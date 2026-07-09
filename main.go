package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// scrapeTimeout bounds each on-demand Omada controller scrape.
const scrapeTimeout = 20 * time.Second

type appConfig struct {
	OmadaURL           string
	SiteName           string
	ClientID           string
	ClientSecret       string
	ListenAddr         string
	LogLevel           string
	InsecureSkipVerify bool
}

func loadConfigFromEnv() (appConfig, error) {
	cfg := appConfig{
		OmadaURL:     os.Getenv("OMADA_URL"),
		SiteName:     os.Getenv("OMADA_SITE_NAME"),
		ClientID:     os.Getenv("OMADA_CLIENT_ID"),
		ClientSecret: os.Getenv("OMADA_CLIENT_SECRET"),
		ListenAddr:   getEnvDefault("LISTEN_ADDR", ":8080"),
		LogLevel:     getEnvDefault("LOG_LEVEL", "info"),
	}

	var missing []string
	if cfg.OmadaURL == "" {
		missing = append(missing, "OMADA_URL")
	}
	if cfg.SiteName == "" {
		missing = append(missing, "OMADA_SITE_NAME")
	}
	if cfg.ClientID == "" {
		missing = append(missing, "OMADA_CLIENT_ID")
	}
	if cfg.ClientSecret == "" {
		missing = append(missing, "OMADA_CLIENT_SECRET")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	skip, err := parseBoolDefault(os.Getenv("OMADA_INSECURE_SKIP_VERIFY"), false)
	if err != nil {
		return cfg, fmt.Errorf("invalid OMADA_INSECURE_SKIP_VERIFY: %w", err)
	}
	cfg.InsecureSkipVerify = skip

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseBoolDefault(v string, def bool) (bool, error) {
	if v == "" {
		return def, nil
	}
	return strconv.ParseBool(v)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func main() {
	cfg, err := loadConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "omada-exporter:", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)

	client, err := NewClient(context.Background(), Config{
		BaseURL:            cfg.OmadaURL,
		SiteName:           cfg.SiteName,
		ClientID:           cfg.ClientID,
		ClientSecret:       cfg.ClientSecret,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}, logger)
	if err != nil {
		logger.Error("failed to initialize omada client", "error", err)
		os.Exit(1)
	}

	collector := NewCollector(client, logger, scrapeTimeout)
	prometheus.MustRegister(collector)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	logger.Info("starting omada-exporter", "listen_addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		logger.Error("http server failed", "error", err)
		os.Exit(1)
	}
}
