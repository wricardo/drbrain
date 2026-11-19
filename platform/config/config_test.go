package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("DRBRAIN_ADDR", ":9999")
	t.Setenv("DRBRAIN_GAMES", "mazes, motor ,")
	t.Setenv("DRBRAIN_PUBLIC_URL", "https://drbrain.test")
	t.Setenv("DRBRAIN_STATIC_DIR", "web/build")
	t.Setenv("DRBRAIN_SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("DRBRAIN_DEBUG", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9999" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if got := cfg.EnabledGames(); len(got) != 2 || got[0] != "mazes" || got[1] != "motor" {
		t.Errorf("EnabledGames = %v, want [mazes motor]", got)
	}
	if cfg.PublicURL != "https://drbrain.test" || cfg.StaticDir != "web/build" {
		t.Errorf("URL/dir = %q/%q", cfg.PublicURL, cfg.StaticDir)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 3s", cfg.ShutdownTimeout)
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
}

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"DRBRAIN_ADDR", "DRBRAIN_GAMES", "DRBRAIN_PUBLIC_URL", "DRBRAIN_STATIC_DIR", "DRBRAIN_SHUTDOWN_TIMEOUT", "DRBRAIN_DEBUG"} {
		t.Setenv(key, "") // registers restoration of the original value
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	// With the variables absent, envconfig applies every default.
	// the deliberately empty optional fields.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 10s", cfg.ShutdownTimeout)
	}
	if got := cfg.EnabledGames(); got != nil {
		t.Errorf("EnabledGames = %v, want nil (all)", got)
	}
}

func TestValidateRejectsBadTimeout(t *testing.T) {
	cfg := Config{Addr: ":8080", StaticDir: "static", ShutdownTimeout: -time.Second}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a negative timeout")
	}
}
