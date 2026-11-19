package delivery

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/host"
)

func TestGameMetadata(t *testing.T) {
	g := New()
	if g.Name() != "delivery" || g.Prefix() != "/delivery" {
		t.Errorf("name/prefix = %q/%q", g.Name(), g.Prefix())
	}
	want := []string{"/graphql", "/graphql/"}
	got := g.API()
	if len(got) != len(want) {
		t.Fatalf("API = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("API[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestMalformedCityFailsBeforeBind pins the startup order: the city is parsed
// in Mount, before the host opens a port, and the error names the game.
func TestMalformedCityFailsBeforeBind(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "city.json")
	if err := os.WriteFile(bad, []byte(`{"name":"broken","layout":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DELIVERY_CITY", bad)
	t.Setenv("DELIVERY_SESSIONS_DIR", filepath.Join(dir, "sessions"))

	addr := freeAddr(t)
	cfg := config.Config{Addr: addr, StaticDir: t.TempDir(), ShutdownTimeout: time.Second}
	reg, err := host.NewRegistry(New())
	if err != nil {
		t.Fatal(err)
	}
	h := host.New(cfg, reg, nil)
	err = h.Run(context.Background())
	if err == nil {
		t.Fatal("Run succeeded with a malformed city")
	}
	if !strings.Contains(err.Error(), "delivery") || !strings.Contains(err.Error(), "city") {
		t.Errorf("error = %q, want it to name delivery and the city", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s was bound despite the failure: %v", addr, err)
	}
	ln.Close()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}
