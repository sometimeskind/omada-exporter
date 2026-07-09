package main

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestClient(t *testing.T, baseURL, siteName string) *Client {
	t.Helper()
	cfg := Config{BaseURL: baseURL, SiteName: siteName, ClientID: "test-id", ClientSecret: "test-secret"}
	client, err := NewClient(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestNewClient_ResolvesControllerAndSite(t *testing.T) {
	srv, fs := newFixtureServer(false)
	defer srv.Close()

	client := newTestClient(t, srv.URL, testSiteName)
	if client.omadacID != testOmadacID {
		t.Errorf("omadacID = %q, want %q", client.omadacID, testOmadacID)
	}
	if client.siteID != testSiteID {
		t.Errorf("siteID = %q, want %q", client.siteID, testSiteID)
	}
	if got := atomic.LoadInt32(&fs.tokenMints); got != 1 {
		t.Errorf("tokenMints = %d, want 1", got)
	}
}

func TestNewClient_UnknownSiteFailsFast(t *testing.T) {
	srv, _ := newFixtureServer(false)
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, SiteName: "NoSuchSite", ClientID: "id", ClientSecret: "secret"}
	if _, err := NewClient(context.Background(), cfg, testLogger()); err == nil {
		t.Fatal("expected error for unknown site name, got nil")
	}
}

func TestGetDevices_RefreshesExpiredTokenAndRetries(t *testing.T) {
	srv, fs := newFixtureServer(true)
	defer srv.Close()

	client := newTestClient(t, srv.URL, testSiteName)

	devices, err := client.GetDevices(context.Background())
	if err != nil {
		t.Fatalf("GetDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(devices))
	}
	if got := atomic.LoadInt32(&fs.tokenMints); got != 2 {
		t.Errorf("tokenMints = %d, want 2 (initial mint + re-mint after -44112)", got)
	}
	if got := atomic.LoadInt32(&fs.deviceRequests); got != 2 {
		t.Errorf("deviceRequests = %d, want 2 (failed attempt + retry)", got)
	}
}

func TestGetDevices_ParsesFields(t *testing.T) {
	srv, _ := newFixtureServer(false)
	defer srv.Close()
	client := newTestClient(t, srv.URL, testSiteName)

	devices, err := client.GetDevices(context.Background())
	if err != nil {
		t.Fatalf("GetDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(devices))
	}
	sw := devices[0]
	if sw.Mac != "AA-BB-CC-00-00-01" || sw.Type != "switch" || sw.Status != 1 || sw.Uptime != "3day(s) 1h 46m 13s" {
		t.Errorf("unexpected switch device: %+v", sw)
	}
}

func TestGetPoEUsage(t *testing.T) {
	srv, _ := newFixtureServer(false)
	defer srv.Close()
	client := newTestClient(t, srv.URL, testSiteName)

	poe, err := client.GetPoEUsage(context.Background())
	if err != nil {
		t.Fatalf("GetPoEUsage: %v", err)
	}
	if len(poe) != 1 {
		t.Fatalf("len(poe) = %d, want 1", len(poe))
	}
	if poe[0].TotalPercentUsed != 22.19 {
		t.Errorf("TotalPercentUsed = %v, want 22.19", poe[0].TotalPercentUsed)
	}
	if len(poe[0].PoePorts) != 2 {
		t.Fatalf("len(poePorts) = %d, want 2", len(poe[0].PoePorts))
	}
}

func TestGetClients(t *testing.T) {
	srv, _ := newFixtureServer(false)
	defer srv.Close()
	client := newTestClient(t, srv.URL, testSiteName)

	clients, err := client.GetClients(context.Background())
	if err != nil {
		t.Fatalf("GetClients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("len(clients) = %d, want 1", len(clients))
	}
	if clients[0].SSID != "MySSID" || clients[0].ApName != "ap-bedroom" {
		t.Errorf("unexpected client: %+v", clients[0])
	}
}
