// Package config loads the daemon's runtime configuration. The HTTP daemon is
// a loopback-only sidecar: it binds 127.0.0.1, takes no public traffic, and
// reads everything it needs from the environment with sane defaults so it can
// boot with zero configuration in development.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// DefaultHost is loopback only. The daemon must never bind a public
	// interface — it speaks to the Electron main process over 127.0.0.1.
	DefaultHost = "127.0.0.1"
	// DefaultPort is the single port the whole surface (REST, SSE, WS, static)
	// is served from. Single-port keeps it same-origin: no CORS, one lifecycle.
	DefaultPort = 3001
	// DefaultRequestTimeout bounds a single request. Long-lived surfaces (SSE,
	// WS) are mounted outside this timeout; it guards the REST surface only.
	DefaultRequestTimeout = 60 * time.Second
	// DefaultShutdownTimeout is the hard cap on graceful shutdown. After this
	// the process exits even if connections are still draining.
	DefaultShutdownTimeout = 10 * time.Second
)

// Config is the fully-resolved daemon configuration. It is immutable once
// built by Load.
type Config struct {
	// Host is the bind address. Always loopback in normal operation.
	Host string
	// Port is the TCP port to bind. The daemon fails fast if it is taken.
	Port int
	// Env is the deployment environment label ("development" | "production").
	// It only affects log verbosity / formatting, never bind behaviour.
	Env string
	// RequestTimeout bounds REST request handling.
	RequestTimeout time.Duration
	// ShutdownTimeout is the hard graceful-shutdown deadline.
	ShutdownTimeout time.Duration
	// RunFilePath is where the PID + port handshake file (running.json) is
	// written so the Electron supervisor can discover and reap the daemon.
	RunFilePath string
}

// Addr returns the host:port the HTTP server binds.
func (c Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// IsProduction reports whether the daemon is running in production mode.
func (c Config) IsProduction() bool { return c.Env == "production" }

// Load resolves configuration from the environment, applying defaults. It
// returns an error only for values that are present but malformed (e.g. a
// non-numeric AO_PORT); missing values fall back to defaults.
//
// Recognised variables:
//
//	AO_HOST              bind host           (default 127.0.0.1)
//	AO_PORT              bind port           (default 3001)
//	AO_ENV               environment label   (default development)
//	AO_REQUEST_TIMEOUT   per-request timeout (Go duration, default 60s)
//	AO_SHUTDOWN_TIMEOUT  shutdown deadline   (Go duration, default 10s)
//	AO_RUN_FILE          running.json path   (default <state-dir>/running.json)
func Load() (Config, error) {
	cfg := Config{
		Host:            getEnv("AO_HOST", DefaultHost),
		Port:            DefaultPort,
		Env:             getEnv("AO_ENV", "development"),
		RequestTimeout:  DefaultRequestTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
	}

	if raw := os.Getenv("AO_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AO_PORT %q: %w", raw, err)
		}
		if port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("invalid AO_PORT %d: out of range 1-65535", port)
		}
		cfg.Port = port
	}

	if raw := os.Getenv("AO_REQUEST_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AO_REQUEST_TIMEOUT %q: %w", raw, err)
		}
		cfg.RequestTimeout = d
	}

	if raw := os.Getenv("AO_SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AO_SHUTDOWN_TIMEOUT %q: %w", raw, err)
		}
		cfg.ShutdownTimeout = d
	}

	runFile, err := resolveRunFilePath()
	if err != nil {
		return Config{}, err
	}
	cfg.RunFilePath = runFile

	return cfg, nil
}

// resolveRunFilePath picks where running.json lives. An explicit AO_RUN_FILE
// wins; otherwise it sits under the per-user state directory so multiple repos
// share one supervisor handshake location.
func resolveRunFilePath() (string, error) {
	if p, ok := os.LookupEnv("AO_RUN_FILE"); ok && p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(dir, "agent-orchestrator", "running.json"), nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
