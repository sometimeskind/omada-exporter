# omada-exporter

A Prometheus exporter for TP-Link Omada controllers that talks to the
**official Omada OpenAPI** (not the legacy `/api/v2` web API) and works with
**Agile Series switches** (e.g. ES208GP).

## Why not one of the existing exporters?

The popular `stanislawhorna/omada-exporter-go` and `charlie-haley/omada_exporter`
both call the per-switch endpoint `GET .../switches/{mac}`, which Agile
Series switches reject with `errorCode -39742` ("The Agile Series Switch
should use the corresponding path"). Both treat that as fatal and abort the
entire scrape, so an Agile switch on your network means **zero** device
metrics.

This exporter never calls the per-switch endpoint. It only uses site-level
endpoints (`/devices`, `/dashboard/poe-usage`, `/clients`), all of which
Agile switches accept.

Verified against a live controller: Omada Controller `6.2.10.17` (mbentley
image), one ES208GP switch + two EAP610 APs.

## Setup

### 1. Create an OpenAPI client credential

In the controller UI: **Settings → Platform Integration → OpenAPI → Client
Credentials mode**, create an application with the **Administrator** role.

A Viewer role is not enough — it returns `errorCode -1505` ("current user
does not have permissions to access this site") on every data call.

You only need the resulting **Client ID** and **Client Secret**; no
username/password is required or used.

> **Only one instance may use a given client ID at a time.** Omada
> invalidates the previous access token whenever a new one is minted for the
> same client ID. Running two exporters (or an exporter and some other tool)
> against the same client ID causes constant token churn and intermittent
> auth failures. Create a separate OpenAPI application per consumer.

### 2. Configuration

All configuration is via environment variables:

| Var | Required | Default | Notes |
|---|---|---|---|
| `OMADA_URL` | yes | — | Controller base URL, including port, e.g. `https://omada.example.com:8043` |
| `OMADA_SITE_NAME` | yes | — | Site name as shown in the UI, e.g. `Default`. Resolved to a site id at startup. |
| `OMADA_CLIENT_ID` | yes | — | OpenAPI client id |
| `OMADA_CLIENT_SECRET` | yes | — | OpenAPI client secret |
| `LISTEN_ADDR` | no | `:8080` | Metrics listen address |
| `LOG_LEVEL` | no | `info` | `debug` / `info` / `warn` / `error` |
| `OMADA_INSECURE_SKIP_VERIFY` | no | `false` | Skip TLS verification (self-signed controllers) |

The exporter resolves the controller id and site id, and mints an initial
access token, at startup — it fails fast if the URL, credentials, or site
name are wrong rather than failing silently on first scrape.

### 3. Run it

```sh
docker run -p 8080:8080 \
  -e OMADA_URL=https://omada.example.com:8043 \
  -e OMADA_SITE_NAME=Default \
  -e OMADA_CLIENT_ID=... \
  -e OMADA_CLIENT_SECRET=... \
  ghcr.io/sometimeskind/omada-exporter:latest
```

### 4. Example Prometheus scrape config

```yaml
scrape_configs:
  - job_name: omada
    scrape_interval: 30s
    scrape_timeout: 25s
    static_configs:
      - targets: ["omada-exporter:8080"]
```

Each scrape hits the controller on demand, so `scrape_timeout` should stay
comfortably above the exporter's internal 20s per-scrape budget.

## Metrics

- `omada_device_up`, `omada_device_cpu_percent`, `omada_device_memory_percent`,
  `omada_device_uptime_seconds` — labeled `mac,name,model,type`
- `omada_switch_poe_power_budget_watts`, `omada_switch_poe_power_used_watts`,
  `omada_switch_poe_percent_used` — labeled `switch_mac,switch_name`
- `omada_switch_port_poe_power_watts`, `omada_switch_port_poe_enabled` —
  labeled `switch_mac,switch_name,port`
- `omada_clients_total`, `omada_ap_clients_total{ap_mac,ap_name}`,
  `omada_ssid_clients_total{ssid}`
- `omada_client_traffic_down_bytes`, `omada_client_traffic_up_bytes` —
  labeled `mac,name,ssid,ap_name,vendor`
- `omada_client_signal_dbm`, `omada_client_snr_db`, `omada_client_rx_rate_kbps`,
  `omada_client_tx_rate_kbps` — labeled `mac,name,ssid,ap_name`
- `omada_scrape_success`, `omada_scrape_duration_seconds`

If a scrape of the controller fails, the exporter still responds with HTTP
200 and `omada_scrape_success 0` (no device series) — the Prometheus
target's own `up` reflects exporter liveness, while `omada_scrape_success`
reflects controller reachability.

## Out of scope

- **Logs.** This exporter is metrics-only; it does not ship logs.
- **Deployment.** Kubernetes manifests, secrets, dashboards, and alerts are
  expected to live in a separate deployment repo that references the
  published image.

## Development

```sh
go build ./...
go test ./...
```
