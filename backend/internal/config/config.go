// Package config loads the daemon's runtime configuration. The HTTP daemon is
// a loopback-only sidecar: it binds 127.0.0.1, takes no public traffic, and
// reads everything it needs from the environment with sane defaults so it can
// boot with zero configuration in development.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// LoopbackHost is the only host the daemon ever binds. There is deliberately
	// no AO_HOST env var: the daemon has no auth/CORS/TLS and a stray
	// AO_HOST=0.0.0.0 would turn it into a public no-auth service. If a
	// non-default loopback (e.g. ::1, 127.0.0.2) is ever needed, add it back with
	// an IsLoopback() validator — not a raw env read.
	LoopbackHost = "127.0.0.1"
	// DefaultPort is the single port for REST, terminal mux, health, and control.
	DefaultPort = 3001
	// DefaultRequestTimeout bounds a single REST request. Long-lived terminal mux
	// connections are mounted outside this timeout.
	DefaultRequestTimeout = 60 * time.Second
	// DefaultShutdownTimeout is the hard cap on graceful shutdown. After this
	// the process exits even if connections are still draining.
	DefaultShutdownTimeout = 10 * time.Second
	// DefaultAgent is the agent adapter id the daemon wires when AO_AGENT is
	// unset. It matches the claude-code adapter's manifest id.
	DefaultAgent = "claude-code"
)

// DefaultAllowedOrigins are the browser origins the daemon's CORS boundary
// trusts, beyond loopback-served content (which the middleware always trusts —
// local pages can reach the no-auth daemon directly anyway). The daemon has no
// auth, so every entry must be an origin web content cannot present:
// app://renderer is the packaged Electron renderer, served from a custom
// scheme only the desktop app registers — no website can bear it. The opaque
// "null" origin (file:// pages, sandboxed iframes on any website) must never
// be added.
var DefaultAllowedOrigins = []string{
	"app://renderer",
}

// Config is the fully-resolved daemon configuration. It is immutable once
// built by Load.
type Config struct {
	// Host is the bind address. Always loopback — see LoopbackHost.
	Host string
	// Port is the TCP port to bind. The daemon fails fast if it is taken.
	Port int
	// RequestTimeout bounds REST request handling.
	RequestTimeout time.Duration
	// ShutdownTimeout is the hard graceful-shutdown deadline.
	ShutdownTimeout time.Duration
	// RunFilePath is where the PID + port handshake file (running.json) is
	// written so the Electron supervisor can discover and reap the daemon.
	RunFilePath string
	// DataDir is the directory holding durable SQLite state: DB and WAL files.
	// It is created on first use by the storage layer.
	DataDir string
	// Agent is the id of the agent adapter the daemon wires into the Session
	// Manager (see DefaultAgent). Selected by AO_AGENT; startSession fails fast
	// if no adapter with this id is registered.
	Agent string
	// AllowedOrigins are the browser origins granted CORS read access (see
	// DefaultAllowedOrigins). Overridden by AO_ALLOWED_ORIGINS.
	AllowedOrigins []string
}

// Addr returns the host:port the HTTP server binds. It uses net.JoinHostPort so
// the result is correct for IPv6 literals as well as IPv4 / hostnames.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Load resolves configuration from the environment, applying defaults. It
// returns an error only for values that are present but malformed (e.g. a
// non-numeric AO_PORT); missing values fall back to defaults.
//
// Recognised variables:
//
//	AO_PORT              bind port           (default 3001)
//	AO_REQUEST_TIMEOUT   per-request timeout (Go duration > 0, default 60s)
//	AO_SHUTDOWN_TIMEOUT  shutdown deadline   (Go duration > 0, default 10s)
//	AO_RUN_FILE          running.json path   (default ~/.ao/running.json)
//	AO_DATA_DIR          durable state dir   (default ~/.ao/data)
//	AO_AGENT             agent adapter id    (default claude-code)
//	AO_ALLOWED_ORIGINS   CORS origins, comma-separated (default DefaultAllowedOrigins)
//
// The bind host is not configurable: the daemon is loopback-only by design.
func Load() (Config, error) {
	cfg := Config{
		Host:            LoopbackHost,
		Port:            DefaultPort,
		RequestTimeout:  DefaultRequestTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
		Agent:           DefaultAgent,
		AllowedOrigins:  DefaultAllowedOrigins,
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
		d, err := parsePositiveDuration("AO_REQUEST_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.RequestTimeout = d
	}

	if raw := os.Getenv("AO_SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("AO_SHUTDOWN_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.ShutdownTimeout = d
	}

	if raw := os.Getenv("AO_AGENT"); raw != "" {
		cfg.Agent = raw
	}

	if raw, ok := os.LookupEnv("AO_ALLOWED_ORIGINS"); ok && raw != "" {
		// Explicit override replaces the defaults entirely so a deployment can
		// also narrow the list. The "null" origin is rejected, never silently
		// dropped: an operator allowing it would open the no-auth daemon to
		// every sandboxed iframe on the web.
		origins := make([]string, 0, 4)
		for _, origin := range strings.Split(raw, ",") {
			origin = strings.TrimSpace(origin)
			if origin == "" {
				continue
			}
			if origin == "null" || origin == "*" {
				return Config{}, fmt.Errorf("invalid AO_ALLOWED_ORIGINS entry %q: wildcard and null origins are not allowed", origin)
			}
			origins = append(origins, origin)
		}
		cfg.AllowedOrigins = origins
	}

	// One-time migration of pre-#233 state from the old os.UserConfigDir()
	// location to the canonical ~/.ao home. Only attempted in the fully-default
	// case: an explicit AO_DATA_DIR or AO_RUN_FILE means the operator is directing
	// paths, so we must not move data out from under them.
	if os.Getenv("AO_DATA_DIR") == "" && os.Getenv("AO_RUN_FILE") == "" {
		if legacy, err := migrateLegacyStateDir(); err != nil {
			slog.Warn("config: could not migrate pre-#233 state to ~/.ao; your previous "+
				"projects and sessions remain at the old location and must be moved by hand",
				"legacy", legacy, "err", err)
		} else if legacy != "" {
			slog.Info("config: migrated pre-#233 state dir to ~/.ao", "from", legacy)
		}
	}

	runFile, err := resolveRunFilePath()
	if err != nil {
		return Config{}, err
	}
	cfg.RunFilePath = runFile

	dataDir, err := resolveDataDir()
	if err != nil {
		return Config{}, err
	}
	cfg.DataDir = dataDir

	return cfg, nil
}

// parsePositiveDuration rejects zero and negative durations: a zero
// RequestTimeout would expire every request instantly, and a non-positive
// ShutdownTimeout would defeat graceful shutdown.
func parsePositiveDuration(name, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be > 0", name, raw)
	}
	return d, nil
}

// resolveRunFilePath picks where running.json lives. An explicit AO_RUN_FILE
// wins; otherwise it sits under the canonical AO home directory so the CLI and
// Electron supervisor share one handshake location.
func resolveRunFilePath() (string, error) {
	if p, ok := os.LookupEnv("AO_RUN_FILE"); ok && p != "" {
		return p, nil
	}
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "running.json"), nil
}

// resolveDataDir picks where durable state (the SQLite DB) lives. An explicit
// AO_DATA_DIR wins; otherwise it defaults under the same canonical AO home
// directory as the run-file.
func resolveDataDir() (string, error) {
	if p, ok := os.LookupEnv("AO_DATA_DIR"); ok && p != "" {
		return p, nil
	}
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "data"), nil
}

func defaultStateDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(homeDir, ".ao"), nil
}

// legacyStateDirName is the directory the daemon used under os.UserConfigDir()
// before #233 moved the canonical state home to ~/.ao (e.g.
// ~/.config/agent-orchestrator on Linux, ~/Library/Application Support/
// agent-orchestrator on macOS). Its internal layout (data/, running.json) is
// identical to the new home, so a whole-directory move preserves everything.
const legacyStateDirName = "agent-orchestrator"

// migrateLegacyStateDir performs a one-time, best-effort move of pre-#233 state
// into the canonical ~/.ao home. It runs only when the canonical home does not
// yet exist and the legacy directory does, so an upgrading user keeps their
// projects/sessions instead of silently booting against an empty database.
//
// It never overwrites an existing ~/.ao, and on any failure it leaves the legacy
// data untouched (and therefore still recoverable). The returned string is the
// legacy path that was moved, or "" when there was nothing to migrate.
func migrateLegacyStateDir() (string, error) {
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	switch _, statErr := os.Stat(stateDir); {
	case statErr == nil:
		return "", nil // canonical home already exists — never clobber it
	case !errors.Is(statErr, os.ErrNotExist):
		return "", fmt.Errorf("stat %s: %w", stateDir, statErr)
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		//nolint:nilerr // no resolvable config dir simply means no legacy state to migrate
		return "", nil
	}
	legacy := filepath.Join(configDir, legacyStateDirName)
	if info, statErr := os.Stat(legacy); statErr != nil || !info.IsDir() {
		//nolint:nilerr // an absent/unreadable legacy dir is not a Load failure — nothing to migrate
		return "", nil
	}

	// Atomic when both live on one filesystem, which is the common case (both
	// under $HOME). A cross-device legacy location fails here and surfaces as a
	// warning rather than a silent, partial copy.
	if err := os.Rename(legacy, stateDir); err != nil {
		return legacy, fmt.Errorf("move %s to %s: %w", legacy, stateDir, err)
	}
	return legacy, nil
}
