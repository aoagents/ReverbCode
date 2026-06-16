package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear every recognised var so we observe pure defaults regardless of the
	// surrounding environment.
	for _, k := range []string{"AO_PORT", "AO_REQUEST_TIMEOUT", "AO_SHUTDOWN_TIMEOUT", "AO_RUN_FILE", "AO_DATA_DIR", "AO_AGENT", "AO_ALLOWED_ORIGINS"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != LoopbackHost {
		t.Errorf("Host = %q, want %q", cfg.Host, LoopbackHost)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %s, want %s", cfg.RequestTimeout, DefaultRequestTimeout)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, DefaultShutdownTimeout)
	}
	if cfg.RunFilePath == "" {
		t.Error("RunFilePath is empty, want a resolved default path")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantRunFilePath := filepath.Join(homeDir, ".ao", "running.json")
	if cfg.RunFilePath != wantRunFilePath {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, wantRunFilePath)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir is empty, want a resolved default path")
	}
	wantDataDir := filepath.Join(homeDir, ".ao", "data")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("AO_PORT", "4002")
	t.Setenv("AO_REQUEST_TIMEOUT", "5s")
	t.Setenv("AO_SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("AO_RUN_FILE", "/tmp/ao-test-running.json")
	t.Setenv("AO_DATA_DIR", "/tmp/ao-test-data")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr() != "127.0.0.1:4002" {
		t.Errorf("Addr() = %q, want 127.0.0.1:4002", cfg.Addr())
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Errorf("RequestTimeout = %s, want 5s", cfg.RequestTimeout)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 3s", cfg.ShutdownTimeout)
	}
	if cfg.RunFilePath != "/tmp/ao-test-running.json" {
		t.Errorf("RunFilePath = %q, want /tmp/ao-test-running.json", cfg.RunFilePath)
	}
	if cfg.DataDir != "/tmp/ao-test-data" {
		t.Errorf("DataDir = %q, want /tmp/ao-test-data", cfg.DataDir)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"non-numeric port", map[string]string{"AO_PORT": "abc"}},
		{"port out of range", map[string]string{"AO_PORT": "70000"}},
		{"bad request timeout", map[string]string{"AO_REQUEST_TIMEOUT": "soon"}},
		{"bad shutdown timeout", map[string]string{"AO_SHUTDOWN_TIMEOUT": "later"}},
		{"zero request timeout", map[string]string{"AO_REQUEST_TIMEOUT": "0s"}},
		{"negative request timeout", map[string]string{"AO_REQUEST_TIMEOUT": "-1s"}},
		{"zero shutdown timeout", map[string]string{"AO_SHUTDOWN_TIMEOUT": "0s"}},
		{"negative shutdown timeout", map[string]string{"AO_SHUTDOWN_TIMEOUT": "-5s"}},
		{"null origin", map[string]string{"AO_ALLOWED_ORIGINS": "app://renderer,null"}},
		{"wildcard origin", map[string]string{"AO_ALLOWED_ORIGINS": "*"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load() = nil error, want error")
			}
		})
	}
}

// seedLegacyState lays down a fake pre-#233 state dir under the resolved
// os.UserConfigDir() and returns the legacy root plus the marker DB path.
func seedLegacyState(t *testing.T) (legacyRoot, dbPath string) {
	t.Helper()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyRoot = filepath.Join(configDir, legacyStateDirName)
	dbPath = filepath.Join(legacyRoot, "data", "ao.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		t.Fatalf("seed legacy data dir: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("legacy-db-marker"), 0o600); err != nil {
		t.Fatalf("seed legacy db: %v", err)
	}
	return legacyRoot, dbPath
}

func TestLegacyStateDirMigration(t *testing.T) {
	// Isolate HOME and XDG_CONFIG_HOME so UserHomeDir/UserConfigDir resolve into
	// the temp dir, and clear path overrides so Load takes the default branch.
	t.Run("migrates pre-#233 state when ~/.ao is absent", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)                                         // UserHomeDir on unix
		t.Setenv("USERPROFILE", home)                                  // UserHomeDir on Windows
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))    // UserConfigDir on Linux
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming")) // UserConfigDir on Windows
		t.Setenv("AO_DATA_DIR", "")
		t.Setenv("AO_RUN_FILE", "")

		legacyRoot, _ := seedLegacyState(t)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		// The marker DB now lives under the canonical home...
		migrated := filepath.Join(cfg.DataDir, "ao.db")
		got, err := os.ReadFile(migrated)
		if err != nil {
			t.Fatalf("read migrated db at %s: %v", migrated, err)
		}
		if string(got) != "legacy-db-marker" {
			t.Errorf("migrated db = %q, want legacy-db-marker", got)
		}
		// ...and the legacy directory is gone (it was moved, not copied).
		if _, err := os.Stat(legacyRoot); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("legacy dir still present after migration: stat err = %v", err)
		}
	})

	t.Run("does not clobber an existing ~/.ao", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)                                         // UserHomeDir on unix
		t.Setenv("USERPROFILE", home)                                  // UserHomeDir on Windows
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))    // UserConfigDir on Linux
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming")) // UserConfigDir on Windows
		t.Setenv("AO_DATA_DIR", "")
		t.Setenv("AO_RUN_FILE", "")

		// Canonical home already exists with its own DB.
		canonicalDB := filepath.Join(home, ".ao", "data", "ao.db")
		if err := os.MkdirAll(filepath.Dir(canonicalDB), 0o750); err != nil {
			t.Fatalf("seed canonical: %v", err)
		}
		if err := os.WriteFile(canonicalDB, []byte("canonical-db"), 0o600); err != nil {
			t.Fatalf("seed canonical db: %v", err)
		}
		legacyRoot, legacyDB := seedLegacyState(t)

		if _, err := Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		// Canonical untouched, legacy left in place (recoverable).
		got, _ := os.ReadFile(canonicalDB)
		if string(got) != "canonical-db" {
			t.Errorf("canonical db = %q, want canonical-db (must not be overwritten)", got)
		}
		if _, err := os.Stat(legacyDB); err != nil {
			t.Errorf("legacy db should be left untouched, stat err = %v", err)
		}
		_ = legacyRoot
	})

	t.Run("explicit AO_DATA_DIR disables migration", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)                                         // UserHomeDir on unix
		t.Setenv("USERPROFILE", home)                                  // UserHomeDir on Windows
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))    // UserConfigDir on Linux
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming")) // UserConfigDir on Windows
		t.Setenv("AO_DATA_DIR", filepath.Join(home, "explicit-data"))
		t.Setenv("AO_RUN_FILE", "")

		legacyRoot, legacyDB := seedLegacyState(t)

		if _, err := Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		// Operator-directed paths: legacy data must be left exactly where it is.
		if _, err := os.Stat(legacyDB); err != nil {
			t.Errorf("legacy db moved despite explicit AO_DATA_DIR: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".ao")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("~/.ao created despite explicit AO_DATA_DIR: stat err = %v", err)
		}
		_ = legacyRoot
	})
}

func TestLoadAllowedOrigins(t *testing.T) {
	t.Run("default includes the packaged renderer origin", func(t *testing.T) {
		t.Setenv("AO_ALLOWED_ORIGINS", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		found := false
		for _, origin := range cfg.AllowedOrigins {
			if origin == "app://renderer" {
				found = true
			}
		}
		if !found {
			t.Errorf("AllowedOrigins = %v, want app://renderer included", cfg.AllowedOrigins)
		}
	})

	t.Run("override replaces defaults and trims entries", func(t *testing.T) {
		t.Setenv("AO_ALLOWED_ORIGINS", " app://renderer , http://localhost:9999 ,")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := []string{"app://renderer", "http://localhost:9999"}
		if len(cfg.AllowedOrigins) != len(want) {
			t.Fatalf("AllowedOrigins = %v, want %v", cfg.AllowedOrigins, want)
		}
		for i, origin := range want {
			if cfg.AllowedOrigins[i] != origin {
				t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.AllowedOrigins[i], origin)
			}
		}
	})
}
