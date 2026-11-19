package host_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wricardo/drbrain/platform"
	"github.com/wricardo/drbrain/platform/config"
	"github.com/wricardo/drbrain/platform/host"
)

// fakeGame is the smallest platform.Game: one /graphql route and a counted,
// optionally failing, shutdown.
type fakeGame struct {
	name        string
	prefix      string
	mountErr    error
	shutdownErr error
	delay       time.Duration

	mu        sync.Mutex
	shutdowns int
}

func (g *fakeGame) Name() string   { return g.name }
func (g *fakeGame) Prefix() string { return g.prefix }
func (g *fakeGame) API() []string  { return []string{"/graphql"} }

func (g *fakeGame) Mount(mux *http.ServeMux, _ platform.Deps) error {
	if g.mountErr != nil {
		return g.mountErr
	}
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, g.name)
	})
	return nil
}

func (g *fakeGame) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.shutdowns++
	g.mu.Unlock()
	if g.delay > 0 {
		select {
		case <-time.After(g.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.shutdownErr
}

func (g *fakeGame) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.shutdowns
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Addr:            "127.0.0.1:0",
		StaticDir:       t.TempDir(),
		ShutdownTimeout: 5 * time.Second,
	}
}

func startHost(t *testing.T, cfg config.Config, games ...platform.Game) (*host.Host, context.CancelFunc, <-chan error) {
	t.Helper()
	reg, err := host.NewRegistry(games...)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	h := host.New(cfg, reg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	select {
	case <-h.Ready():
	case err := <-done:
		cancel()
		t.Fatalf("host exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("host did not become ready")
	}
	return h, cancel, done
}

func TestServesMountedGames(t *testing.T) {
	cfg := testConfig(t)
	a := &fakeGame{name: "alpha", prefix: "/alpha"}
	b := &fakeGame{name: "beta", prefix: "/beta"}
	h, cancel, done := startHost(t, cfg, a, b)
	defer cancel()

	for _, name := range []string{"alpha", "beta"} {
		resp, err := http.Get("http://" + h.Addr() + "/" + name + "/graphql")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != name {
			t.Errorf("%s body = %q, want %q", name, body, name)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestShutdownIsParallelAndExactlyOnce(t *testing.T) {
	cfg := testConfig(t)
	a := &fakeGame{name: "alpha", prefix: "/alpha", delay: 500 * time.Millisecond}
	b := &fakeGame{name: "beta", prefix: "/beta", delay: 500 * time.Millisecond}
	_, cancel, done := startHost(t, cfg, a, b)

	start := time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed >= time.Second {
		t.Errorf("shutdown took %s for two 500ms games; not parallel", elapsed)
	}
	if a.count() != 1 || b.count() != 1 {
		t.Errorf("shutdown counts = %d/%d, want 1/1", a.count(), b.count())
	}
}

func TestShutdownFailureStillSavesOtherGames(t *testing.T) {
	cfg := testConfig(t)
	bad := &fakeGame{name: "alpha", prefix: "/alpha", shutdownErr: errors.New("disk full")}
	good := &fakeGame{name: "beta", prefix: "/beta"}
	_, cancel, done := startHost(t, cfg, bad, good)

	cancel()
	err := <-done
	if err == nil {
		t.Fatal("run returned nil, want the save error")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error = %q, want it to name alpha and disk full", err)
	}
	if bad.count() != 1 || good.count() != 1 {
		t.Errorf("shutdown counts = %d/%d, want 1/1", bad.count(), good.count())
	}
}

func TestMountErrorLeavesPortFree(t *testing.T) {
	addr := freeAddr(t)
	cfg := testConfig(t)
	cfg.Addr = addr
	bad := &fakeGame{name: "alpha", prefix: "/alpha", mountErr: errors.New("bad map")}

	reg, err := host.NewRegistry(bad)
	if err != nil {
		t.Fatal(err)
	}
	h := host.New(cfg, reg, nil)
	err = h.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad map") {
		t.Fatalf("Run error = %v, want the mount error", err)
	}
	select {
	case <-h.Ready():
		t.Fatal("host became ready despite mount failure")
	default:
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s was bound after a failed mount: %v", addr, err)
	}
	ln.Close()
}

func TestUnknownGameFailsBeforeBinding(t *testing.T) {
	addr := freeAddr(t)
	cfg := testConfig(t)
	cfg.Addr = addr
	cfg.Games = "nope"

	reg, err := host.NewRegistry(&fakeGame{name: "alpha", prefix: "/alpha"})
	if err != nil {
		t.Fatal(err)
	}
	h := host.New(cfg, reg, nil)
	err = h.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unknown game") {
		t.Fatalf("Run error = %v, want unknown game", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s was bound: %v", addr, err)
	}
	ln.Close()
}

// TestSIGTERMShutsDownEachGameOnce exercises the same signal path cmd/drbrain
// wires to Host.Run.
func TestSIGTERMShutsDownEachGameOnce(t *testing.T) {
	cfg := testConfig(t)
	a := &fakeGame{name: "alpha", prefix: "/alpha"}
	b := &fakeGame{name: "beta", prefix: "/beta"}
	reg, err := host.NewRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	h := host.New(cfg, reg, nil)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	select {
	case <-h.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("host did not become ready")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop after SIGTERM")
	}
	if a.count() != 1 || b.count() != 1 {
		t.Errorf("shutdown counts = %d/%d, want 1/1", a.count(), b.count())
	}
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
