package main

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// omadaClient is the subset of *Client the Collector depends on, so tests
// can supply fixtures without standing up an HTTP server.
type omadaClient interface {
	GetDevices(ctx context.Context) ([]Device, error)
	GetPoEUsage(ctx context.Context) ([]SwitchPoE, error)
	GetClients(ctx context.Context) ([]ConnectedClient, error)
}

// Collector scrapes an Omada controller on demand, once per call to
// Collect, and exposes the results as Prometheus metrics.
type Collector struct {
	client        omadaClient
	logger        *slog.Logger
	scrapeTimeout time.Duration

	deviceUp       *prometheus.Desc
	deviceCPU      *prometheus.Desc
	deviceMem      *prometheus.Desc
	deviceUptime   *prometheus.Desc
	poeBudget      *prometheus.Desc
	poeUsed        *prometheus.Desc
	poePercent     *prometheus.Desc
	portPoePower   *prometheus.Desc
	portPoeEnabled *prometheus.Desc
	clientsTotal   *prometheus.Desc
	apClientsTotal *prometheus.Desc
	ssidClients    *prometheus.Desc
	clientTrafDown *prometheus.Desc
	clientTrafUp   *prometheus.Desc
	clientSignal   *prometheus.Desc
	clientSNR      *prometheus.Desc
	clientRxRate   *prometheus.Desc
	clientTxRate   *prometheus.Desc
	scrapeSuccess  *prometheus.Desc
	scrapeDuration *prometheus.Desc
}

// NewCollector builds a Collector that scrapes client with the given
// per-scrape timeout.
func NewCollector(client omadaClient, logger *slog.Logger, scrapeTimeout time.Duration) *Collector {
	deviceLabels := []string{"mac", "name", "model", "type"}
	switchLabels := []string{"switch_mac", "switch_name"}
	portLabels := []string{"switch_mac", "switch_name", "port"}
	clientLabels := []string{"mac", "name", "ssid", "ap_name"}
	clientTrafficLabels := []string{"mac", "name", "ssid", "ap_name", "vendor"}

	return &Collector{
		client:        client,
		logger:        logger,
		scrapeTimeout: scrapeTimeout,

		deviceUp:     prometheus.NewDesc("omada_device_up", "Whether the device is online (1) or not (0).", deviceLabels, nil),
		deviceCPU:    prometheus.NewDesc("omada_device_cpu_percent", "Device CPU utilization percent.", deviceLabels, nil),
		deviceMem:    prometheus.NewDesc("omada_device_memory_percent", "Device memory utilization percent.", deviceLabels, nil),
		deviceUptime: prometheus.NewDesc("omada_device_uptime_seconds", "Device uptime in seconds.", deviceLabels, nil),

		poeBudget:      prometheus.NewDesc("omada_switch_poe_power_budget_watts", "Total PoE power budget for the switch, in watts.", switchLabels, nil),
		poeUsed:        prometheus.NewDesc("omada_switch_poe_power_used_watts", "Total PoE power currently drawn on the switch, in watts.", switchLabels, nil),
		poePercent:     prometheus.NewDesc("omada_switch_poe_percent_used", "Percent of PoE power budget currently in use.", switchLabels, nil),
		portPoePower:   prometheus.NewDesc("omada_switch_port_poe_power_watts", "PoE power drawn on this port, in watts.", portLabels, nil),
		portPoeEnabled: prometheus.NewDesc("omada_switch_port_poe_enabled", "Whether PoE is enabled on this port (1) or not (0).", portLabels, nil),

		clientsTotal:   prometheus.NewDesc("omada_clients_total", "Total number of connected clients.", nil, nil),
		apClientsTotal: prometheus.NewDesc("omada_ap_clients_total", "Number of clients connected to this AP.", []string{"ap_mac", "ap_name"}, nil),
		ssidClients:    prometheus.NewDesc("omada_ssid_clients_total", "Number of clients connected to this SSID.", []string{"ssid"}, nil),

		clientTrafDown: prometheus.NewDesc("omada_client_traffic_down_bytes", "Total downstream traffic for this client, in bytes.", clientTrafficLabels, nil),
		clientTrafUp:   prometheus.NewDesc("omada_client_traffic_up_bytes", "Total upstream traffic for this client, in bytes.", clientTrafficLabels, nil),
		clientSignal:   prometheus.NewDesc("omada_client_signal_dbm", "Client signal strength (RSSI), in dBm.", clientLabels, nil),
		clientSNR:      prometheus.NewDesc("omada_client_snr_db", "Client signal-to-noise ratio, in dB.", clientLabels, nil),
		clientRxRate:   prometheus.NewDesc("omada_client_rx_rate_kbps", "Client receive rate, in kbps.", clientLabels, nil),
		clientTxRate:   prometheus.NewDesc("omada_client_tx_rate_kbps", "Client transmit rate, in kbps.", clientLabels, nil),

		scrapeSuccess:  prometheus.NewDesc("omada_scrape_success", "Whether the last scrape of the Omada controller succeeded (1) or not (0).", nil, nil),
		scrapeDuration: prometheus.NewDesc("omada_scrape_duration_seconds", "Duration of the last scrape of the Omada controller, in seconds.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.deviceUp
	ch <- c.deviceCPU
	ch <- c.deviceMem
	ch <- c.deviceUptime
	ch <- c.poeBudget
	ch <- c.poeUsed
	ch <- c.poePercent
	ch <- c.portPoePower
	ch <- c.portPoeEnabled
	ch <- c.clientsTotal
	ch <- c.apClientsTotal
	ch <- c.ssidClients
	ch <- c.clientTrafDown
	ch <- c.clientTrafUp
	ch <- c.clientSignal
	ch <- c.clientSNR
	ch <- c.clientRxRate
	ch <- c.clientTxRate
	ch <- c.scrapeSuccess
	ch <- c.scrapeDuration
}

// Collect implements prometheus.Collector. It scrapes the Omada controller
// on demand. On failure it still emits omada_scrape_success=0 (and no
// device series) rather than returning an error, so the Prometheus target's
// own `up` reflects exporter liveness while omada_scrape_success reflects
// controller reachability.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), c.scrapeTimeout)
	defer cancel()

	success := 1.0

	devices, err := c.client.GetDevices(ctx)
	if err != nil {
		c.logger.Error("failed to fetch devices", "error", err)
		success = 0
	}
	poe, err := c.client.GetPoEUsage(ctx)
	if err != nil {
		c.logger.Error("failed to fetch poe usage", "error", err)
		success = 0
	}
	clients, err := c.client.GetClients(ctx)
	if err != nil {
		c.logger.Error("failed to fetch clients", "error", err)
		success = 0
	}

	if success == 1 {
		c.collectDevices(ch, devices)
		c.collectPoE(ch, poe)
		c.collectClients(ch, clients)
	}

	ch <- prometheus.MustNewConstMetric(c.scrapeSuccess, prometheus.GaugeValue, success)
	ch <- prometheus.MustNewConstMetric(c.scrapeDuration, prometheus.GaugeValue, time.Since(start).Seconds())
}

func (c *Collector) collectDevices(ch chan<- prometheus.Metric, devices []Device) {
	for _, d := range devices {
		labels := []string{d.Mac, d.Name, d.Model, d.Type}

		up := 0.0
		if d.Status == 1 {
			up = 1.0
		}
		ch <- prometheus.MustNewConstMetric(c.deviceUp, prometheus.GaugeValue, up, labels...)
		ch <- prometheus.MustNewConstMetric(c.deviceCPU, prometheus.GaugeValue, d.CPUUtil, labels...)
		ch <- prometheus.MustNewConstMetric(c.deviceMem, prometheus.GaugeValue, d.MemUtil, labels...)

		if seconds, ok := parseUptimeSeconds(d.Uptime); ok {
			ch <- prometheus.MustNewConstMetric(c.deviceUptime, prometheus.GaugeValue, float64(seconds), labels...)
		}
	}
}

func (c *Collector) collectPoE(ch chan<- prometheus.Metric, switches []SwitchPoE) {
	for _, sw := range switches {
		switchLabels := []string{sw.Mac, sw.Name}
		ch <- prometheus.MustNewConstMetric(c.poeBudget, prometheus.GaugeValue, sw.TotalPower, switchLabels...)
		ch <- prometheus.MustNewConstMetric(c.poeUsed, prometheus.GaugeValue, sw.TotalPowerUsed, switchLabels...)
		ch <- prometheus.MustNewConstMetric(c.poePercent, prometheus.GaugeValue, sw.TotalPercentUsed, switchLabels...)

		for _, port := range sw.PoePorts {
			portLabels := []string{sw.Mac, sw.Name, strconv.Itoa(port.PortID)}
			enabled := 0.0
			if port.PoeEnabled {
				enabled = 1.0
			}
			ch <- prometheus.MustNewConstMetric(c.portPoeEnabled, prometheus.GaugeValue, enabled, portLabels...)
			if port.PoeEnabled {
				ch <- prometheus.MustNewConstMetric(c.portPoePower, prometheus.GaugeValue, port.PoePower, portLabels...)
			}
		}
	}
}

func (c *Collector) collectClients(ch chan<- prometheus.Metric, clients []ConnectedClient) {
	ch <- prometheus.MustNewConstMetric(c.clientsTotal, prometheus.GaugeValue, float64(len(clients)))

	type apKey struct{ mac, name string }
	apCounts := make(map[apKey]int)
	ssidCounts := make(map[string]int)

	for _, cl := range clients {
		apCounts[apKey{cl.ApMac, cl.ApName}]++
		ssidCounts[cl.SSID]++

		labels := []string{cl.Mac, cl.Name, cl.SSID, cl.ApName}
		trafficLabels := []string{cl.Mac, cl.Name, cl.SSID, cl.ApName, cl.Vendor}

		ch <- prometheus.MustNewConstMetric(c.clientTrafDown, prometheus.GaugeValue, cl.TrafficDown, trafficLabels...)
		ch <- prometheus.MustNewConstMetric(c.clientTrafUp, prometheus.GaugeValue, cl.TrafficUp, trafficLabels...)
		ch <- prometheus.MustNewConstMetric(c.clientSignal, prometheus.GaugeValue, cl.RSSI, labels...)
		ch <- prometheus.MustNewConstMetric(c.clientSNR, prometheus.GaugeValue, cl.SNR, labels...)
		ch <- prometheus.MustNewConstMetric(c.clientRxRate, prometheus.GaugeValue, cl.RxRate, labels...)
		ch <- prometheus.MustNewConstMetric(c.clientTxRate, prometheus.GaugeValue, cl.TxRate, labels...)
	}

	for k, count := range apCounts {
		ch <- prometheus.MustNewConstMetric(c.apClientsTotal, prometheus.GaugeValue, float64(count), k.mac, k.name)
	}
	for ssid, count := range ssidCounts {
		ch <- prometheus.MustNewConstMetric(c.ssidClients, prometheus.GaugeValue, float64(count), ssid)
	}
}
