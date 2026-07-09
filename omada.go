package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// errTokenExpired is the Omada OpenAPI errorCode returned when the access
// token has expired mid-session.
const errTokenExpired = -44112

// tokenRefreshBuffer is how far ahead of the token's stated expiry we
// proactively refresh it.
const tokenRefreshBuffer = 60 * time.Second

// Config holds the connection parameters for an Omada controller.
type Config struct {
	BaseURL            string
	SiteName           string
	ClientID           string
	ClientSecret       string
	InsecureSkipVerify bool
}

// Client talks to the Omada OpenAPI using the client_credentials grant.
// Callers must not share a single client_id across multiple Client
// instances: Omada invalidates the previous token whenever a new one is
// minted for the same client_id.
type Client struct {
	httpClient   *http.Client
	baseURL      string
	clientID     string
	clientSecret string
	logger       *slog.Logger

	omadacID string
	siteID   string

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// envelope is the standard Omada OpenAPI response wrapper.
type envelope struct {
	ErrorCode int             `json:"errorCode"`
	Msg       string          `json:"msg"`
	Result    json.RawMessage `json:"result"`
}

// pagedResult is the shape of Result for paginated endpoints.
type pagedResult struct {
	TotalRows   int             `json:"totalRows"`
	CurrentPage int             `json:"currentPage"`
	CurrentSize int             `json:"currentSize"`
	Data        json.RawMessage `json:"data"`
}

// Device is a single entry from the /devices endpoint.
type Device struct {
	Type    string  `json:"type"`
	Status  int     `json:"status"`
	CPUUtil float64 `json:"cpuUtil"`
	MemUtil float64 `json:"memUtil"`
	Uptime  string  `json:"uptime"`
	Mac     string  `json:"mac"`
	Name    string  `json:"name"`
	Model   string  `json:"model"`
}

// PoEPort is a single port entry within a SwitchPoE result.
type PoEPort struct {
	PortID       int     `json:"portId"`
	PoeSupported bool    `json:"poeSupported"`
	PoeEnabled   bool    `json:"poeEnabled"`
	PoePower     float64 `json:"poePower"`
	PoePercent   float64 `json:"poePercent"`
}

// SwitchPoE is a single entry from the /dashboard/poe-usage endpoint.
type SwitchPoE struct {
	Mac              string    `json:"mac"`
	Name             string    `json:"name"`
	PortNum          int       `json:"portNum"`
	TotalPower       float64   `json:"totalPower"`
	TotalPowerUsed   float64   `json:"totalPowerUsed"`
	TotalPercentUsed float64   `json:"totalPercentUsed"`
	PoePorts         []PoEPort `json:"poePorts"`
}

// ConnectedClient is a single entry from the /clients endpoint.
type ConnectedClient struct {
	Mac         string  `json:"mac"`
	Name        string  `json:"name"`
	Vendor      string  `json:"vendor"`
	SSID        string  `json:"ssid"`
	ApMac       string  `json:"apMac"`
	ApName      string  `json:"apName"`
	RSSI        float64 `json:"rssi"`
	SNR         float64 `json:"snr"`
	RxRate      float64 `json:"rxRate"`
	TxRate      float64 `json:"txRate"`
	TrafficDown float64 `json:"trafficDown"`
	TrafficUp   float64 `json:"trafficUp"`
}

// NewClient authenticates against the Omada controller and resolves the
// configured site name to a site id. It fails fast if either step fails so
// that misconfiguration is caught at startup rather than on first scrape.
func NewClient(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	c := &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify},
			},
		},
		baseURL:      cfg.BaseURL,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		logger:       logger,
	}

	omadacID, err := c.fetchControllerID(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching controller id: %w", err)
	}
	c.omadacID = omadacID

	if err := c.mintToken(ctx); err != nil {
		return nil, fmt.Errorf("minting initial access token: %w", err)
	}

	siteID, err := c.resolveSiteID(ctx, cfg.SiteName)
	if err != nil {
		return nil, fmt.Errorf("resolving site id: %w", err)
	}
	c.siteID = siteID

	return c, nil
}

func (c *Client) fetchControllerID(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/info", nil)
	if err != nil {
		return "", err
	}
	body, err := c.do(req)
	if err != nil {
		return "", err
	}
	var info struct {
		Result struct {
			ControllerVer string `json:"controllerVer"`
			OmadacID      string `json:"omadacId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("decoding controller info response: %w", err)
	}
	if info.Result.OmadacID == "" {
		return "", fmt.Errorf("controller info response missing omadacId")
	}
	return info.Result.OmadacID, nil
}

func (c *Client) mintToken(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		"omadacId":      c.omadacID,
		"client_id":     c.clientID,
		"client_secret": c.clientSecret,
	})
	if err != nil {
		return err
	}

	u := c.baseURL + "/openapi/authorize/token?grant_type=client_credentials"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	respBody, err := c.do(req)
	if err != nil {
		return err
	}

	var env envelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("decoding token response: %w", err)
	}
	if env.ErrorCode != 0 {
		return fmt.Errorf("minting token: errorCode %d: %s", env.ErrorCode, env.Msg)
	}

	var tok struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int    `json:"expiresIn"`
	}
	if err := json.Unmarshal(env.Result, &tok); err != nil {
		return fmt.Errorf("decoding token result: %w", err)
	}

	ttl := time.Duration(tok.ExpiresIn) * time.Second
	buffer := tokenRefreshBuffer
	if ttl <= buffer {
		buffer = 0
	}

	c.mu.Lock()
	c.token = tok.AccessToken
	c.tokenExpiry = time.Now().Add(ttl - buffer)
	c.mu.Unlock()

	c.logger.Debug("minted omada access token", "expires_in_seconds", tok.ExpiresIn)
	return nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	needsRefresh := c.token == "" || time.Now().After(c.tokenExpiry)
	c.mu.Unlock()
	if needsRefresh {
		return c.mintToken(ctx)
	}
	return nil
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body from %s: %w", req.URL.Path, err)
	}
	return body, nil
}

// authorizedRequest performs a GET against path with an Authorization
// header, retrying once if the token has expired mid-scrape.
func (c *Client) authorizedRequest(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	env, err := c.authorizedGet(ctx, path, query)
	if err != nil {
		return nil, err
	}

	if env.ErrorCode == errTokenExpired {
		c.logger.Warn("omada access token expired mid-scrape, re-minting and retrying", "path", path)
		if err := c.mintToken(ctx); err != nil {
			return nil, fmt.Errorf("re-minting expired token: %w", err)
		}
		env, err = c.authorizedGet(ctx, path, query)
		if err != nil {
			return nil, err
		}
	}

	if env.ErrorCode != 0 {
		return nil, fmt.Errorf("omada api error %d for %s: %s", env.ErrorCode, path, env.Msg)
	}
	return env.Result, nil
}

func (c *Client) authorizedGet(ctx context.Context, path string, query url.Values) (envelope, error) {
	if err := c.ensureToken(ctx); err != nil {
		return envelope{}, err
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Authorization", "AccessToken="+c.currentToken())

	body, err := c.do(req)
	if err != nil {
		return envelope{}, err
	}

	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return envelope{}, fmt.Errorf("decoding response from %s: %w", path, err)
	}
	return env, nil
}

// fetchPaged repeatedly requests path with page/pageSize query parameters,
// accumulating each page's "data" array entries until all rows are read.
func (c *Client) fetchPaged(ctx context.Context, path string) ([]json.RawMessage, error) {
	const pageSize = 100
	var all []json.RawMessage

	for page := 1; ; page++ {
		query := url.Values{
			"page":     {strconv.Itoa(page)},
			"pageSize": {strconv.Itoa(pageSize)},
		}
		raw, err := c.authorizedRequest(ctx, path, query)
		if err != nil {
			return nil, err
		}

		var pr pagedResult
		if err := json.Unmarshal(raw, &pr); err != nil {
			return nil, fmt.Errorf("decoding paged result for %s: %w", path, err)
		}

		var items []json.RawMessage
		if len(pr.Data) > 0 {
			if err := json.Unmarshal(pr.Data, &items); err != nil {
				return nil, fmt.Errorf("decoding data array for %s: %w", path, err)
			}
		}
		all = append(all, items...)

		if len(items) == 0 || pr.CurrentPage*pr.CurrentSize >= pr.TotalRows {
			break
		}
	}

	return all, nil
}

func (c *Client) sitePath(suffix string) string {
	return fmt.Sprintf("/openapi/v1/%s/sites/%s%s", c.omadacID, c.siteID, suffix)
}

func (c *Client) resolveSiteID(ctx context.Context, siteName string) (string, error) {
	path := fmt.Sprintf("/openapi/v1/%s/sites", c.omadacID)
	items, err := c.fetchPaged(ctx, path)
	if err != nil {
		return "", err
	}

	for _, raw := range items {
		var site struct {
			SiteID string `json:"siteId"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(raw, &site); err != nil {
			return "", fmt.Errorf("decoding site entry: %w", err)
		}
		if site.Name == siteName {
			return site.SiteID, nil
		}
	}

	return "", fmt.Errorf("no site named %q found", siteName)
}

// GetDevices returns the device inventory for the configured site.
// This uses the site-level /devices endpoint, which Agile Series switches
// accept (unlike the per-switch /switches/{mac} endpoint).
func (c *Client) GetDevices(ctx context.Context) ([]Device, error) {
	items, err := c.fetchPaged(ctx, c.sitePath("/devices"))
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(items))
	for _, raw := range items {
		var d Device
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("decoding device entry: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// GetPoEUsage returns per-switch PoE budget/usage for the configured site.
// This uses the Agile-safe /dashboard/poe-usage endpoint, whose result is a
// plain array rather than the usual paginated envelope.
func (c *Client) GetPoEUsage(ctx context.Context) ([]SwitchPoE, error) {
	raw, err := c.authorizedRequest(ctx, c.sitePath("/dashboard/poe-usage"), nil)
	if err != nil {
		return nil, err
	}
	var result []SwitchPoE
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decoding poe-usage result: %w", err)
	}
	return result, nil
}

// GetClients returns the connected clients for the configured site.
func (c *Client) GetClients(ctx context.Context) ([]ConnectedClient, error) {
	items, err := c.fetchPaged(ctx, c.sitePath("/clients"))
	if err != nil {
		return nil, err
	}
	clients := make([]ConnectedClient, 0, len(items))
	for _, raw := range items {
		var cl ConnectedClient
		if err := json.Unmarshal(raw, &cl); err != nil {
			return nil, fmt.Errorf("decoding client entry: %w", err)
		}
		clients = append(clients, cl)
	}
	return clients, nil
}
