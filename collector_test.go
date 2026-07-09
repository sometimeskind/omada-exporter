package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gather(t *testing.T, c prometheus.Collector) []*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register collector: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	return mfs
}

func findMetricFamily(mfs []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	if len(m.Label) != len(want) {
		return false
	}
	for _, l := range m.Label {
		if want[l.GetName()] != l.GetValue() {
			return false
		}
	}
	return true
}

func findMetric(t *testing.T, mfs []*dto.MetricFamily, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	mf := findMetricFamily(mfs, name)
	if mf == nil {
		t.Fatalf("metric family %q not found", name)
	}
	for _, m := range mf.Metric {
		if labelsMatch(m, labels) {
			return m
		}
	}
	t.Fatalf("metric %q with labels %v not found", name, labels)
	return nil
}

func TestCollector_Collect_Success(t *testing.T) {
	srv, _ := newFixtureServer(false)
	defer srv.Close()
	client := newTestClient(t, srv.URL, testSiteName)
	collector := NewCollector(client, testLogger(), 5*time.Second)

	mfs := gather(t, collector)

	if v := findMetric(t, mfs, "omada_scrape_success", map[string]string{}).GetGauge().GetValue(); v != 1 {
		t.Errorf("omada_scrape_success = %v, want 1", v)
	}

	switchLabels := map[string]string{"mac": "AA-BB-CC-00-00-01", "name": "switch", "model": "ES208GP v1.0", "type": "switch"}
	if v := findMetric(t, mfs, "omada_device_up", switchLabels).GetGauge().GetValue(); v != 1 {
		t.Errorf("omada_device_up (switch) = %v, want 1", v)
	}

	wantUptime := float64(3*86400 + 1*3600 + 46*60 + 13)
	if v := findMetric(t, mfs, "omada_device_uptime_seconds", switchLabels).GetGauge().GetValue(); v != wantUptime {
		t.Errorf("omada_device_uptime_seconds = %v, want %v", v, wantUptime)
	}

	swPoELabels := map[string]string{"switch_mac": "AA-BB-CC-00-00-01", "switch_name": "switch"}
	if v := findMetric(t, mfs, "omada_switch_poe_percent_used", swPoELabels).GetGauge().GetValue(); v != 22.19 {
		t.Errorf("omada_switch_poe_percent_used = %v, want 22.19", v)
	}

	portLabels := map[string]string{"switch_mac": "AA-BB-CC-00-00-01", "switch_name": "switch", "port": "3"}
	if v := findMetric(t, mfs, "omada_switch_port_poe_power_watts", portLabels).GetGauge().GetValue(); v != 6 {
		t.Errorf("omada_switch_port_poe_power_watts (port 3) = %v, want 6", v)
	}

	// Port 1 has poeEnabled=false, so it must not get a poe power series.
	if mf := findMetricFamily(mfs, "omada_switch_port_poe_power_watts"); mf != nil {
		disabledPortLabels := map[string]string{"switch_mac": "AA-BB-CC-00-00-01", "switch_name": "switch", "port": "1"}
		for _, m := range mf.Metric {
			if labelsMatch(m, disabledPortLabels) {
				t.Errorf("did not expect omada_switch_port_poe_power_watts series for disabled port 1")
			}
		}
	}

	if v := findMetric(t, mfs, "omada_clients_total", map[string]string{}).GetGauge().GetValue(); v != 1 {
		t.Errorf("omada_clients_total = %v, want 1", v)
	}
	if v := findMetric(t, mfs, "omada_ssid_clients_total", map[string]string{"ssid": "MySSID"}).GetGauge().GetValue(); v != 1 {
		t.Errorf("omada_ssid_clients_total = %v, want 1", v)
	}
	if v := findMetric(t, mfs, "omada_ap_clients_total", map[string]string{"ap_mac": "AA-BB-CC-00-00-02", "ap_name": "ap-bedroom"}).GetGauge().GetValue(); v != 1 {
		t.Errorf("omada_ap_clients_total = %v, want 1", v)
	}
}

func TestCollector_Collect_ScrapeFailure(t *testing.T) {
	// Every data endpoint errors out after a successful startup (controller
	// info, token, and site resolution succeed), so the collector should
	// report scrape failure without emitting any device series.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"result":{"controllerVer":"6.2.10.17","omadacId":%q}}`, testOmadacID)
	})
	mux.HandleFunc("/openapi/authorize/token", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errorCode":0,"msg":"ok","result":{"accessToken":"tok","tokenType":"bearer","expiresIn":7200}}`)
	})
	mux.HandleFunc(fmt.Sprintf("/openapi/v1/%s/sites", testOmadacID), func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"errorCode":0,"msg":"Success.","result":{"totalRows":1,"currentPage":1,"currentSize":100,"data":[{"siteId":%q,"name":%q}]}}`, testSiteID, testSiteName)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errorCode":-1,"msg":"boom"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv.URL, testSiteName)
	collector := NewCollector(client, testLogger(), 5*time.Second)

	mfs := gather(t, collector)

	if v := findMetric(t, mfs, "omada_scrape_success", map[string]string{}).GetGauge().GetValue(); v != 0 {
		t.Errorf("omada_scrape_success = %v, want 0", v)
	}
	if mf := findMetricFamily(mfs, "omada_device_up"); mf != nil && len(mf.Metric) != 0 {
		t.Errorf("expected no omada_device_up series on scrape failure, got %d", len(mf.Metric))
	}
}
