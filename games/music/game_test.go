package music

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	musicgraph "github.com/wricardo/drbrain/games/music/graph"
	"github.com/wricardo/drbrain/platform"
)

func TestGameMetadata(t *testing.T) {
	g := New()
	if g.Name() != "music" {
		t.Errorf("Name = %q", g.Name())
	}
	if g.Prefix() != "/music" {
		t.Errorf("Prefix = %q", g.Prefix())
	}
	// GraphQL moved to the platform root; music exposes no game-local API.
	if got := g.API(); len(got) != 0 {
		t.Errorf("API = %v, want none", got)
	}
	if !strings.Contains(g.LLMSTemplate(), "{{.GraphQLURL}}") {
		t.Error("llms template does not render the GraphQL URL")
	}
	if !strings.Contains(g.LLMSTemplate(), "{{.GraphQLWSURL}}") {
		t.Error("llms template does not render the GraphQL websocket URL")
	}
	if resolver, ok := g.GraphQLResolver().(*musicgraph.Resolver); ok && resolver != nil {
		t.Error("GraphQLResolver before Mount should be nil")
	}
}

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadEnvDefaults(t *testing.T) {
	clearEnv(t, "MUSIC_SCORES_DIR", "MUSIC_CUSTOM_SCORES_DIR", "MUSIC_SESSIONS_DIR",
		"MUSIC_MAX_SESSIONS", "MUSIC_SESSION_TTL", "MUSIC_PUBLIC_URL", "MUSIC_DEBUG")
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.ScoresDir != "scores" || e.SessionsDir != "sessions" || e.CustomScoresDir != "" {
		t.Errorf("dirs = %q/%q/%q", e.ScoresDir, e.SessionsDir, e.CustomScoresDir)
	}
	if e.MaxSessions != 1000 {
		t.Errorf("MaxSessions = %d, want 1000", e.MaxSessions)
	}
	if e.SessionTTL != 7*24*time.Hour {
		t.Errorf("SessionTTL = %v, want 168h", e.SessionTTL)
	}
	if e.Debug {
		t.Error("Debug = true by default")
	}
}

func TestLoadEnvPrefixed(t *testing.T) {
	t.Setenv("MUSIC_SCORES_DIR", "custom-scores")
	t.Setenv("MUSIC_CUSTOM_SCORES_DIR", "mine")
	t.Setenv("MUSIC_SESSIONS_DIR", "custom-sessions")
	t.Setenv("MUSIC_MAX_SESSIONS", "7")
	t.Setenv("MUSIC_SESSION_TTL", "90m")
	t.Setenv("MUSIC_PUBLIC_URL", "https://example.test/music")
	t.Setenv("MUSIC_DEBUG", "true")
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.ScoresDir != "custom-scores" || e.CustomScoresDir != "mine" || e.SessionsDir != "custom-sessions" {
		t.Errorf("dirs = %+v", e)
	}
	if e.MaxSessions != 7 || e.SessionTTL != 90*time.Minute || e.PublicURL != "https://example.test/music" || !e.Debug {
		t.Errorf("env = %+v", e)
	}
}

func TestMount_WiresResolverAndShutdownIsIdempotent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MUSIC_SCORES_DIR", filepath.Join(root, "scores"))
	t.Setenv("MUSIC_CUSTOM_SCORES_DIR", filepath.Join(root, "custom-scores"))
	t.Setenv("MUSIC_SESSIONS_DIR", filepath.Join(root, "sessions"))
	t.Setenv("MUSIC_MAX_SESSIONS", "10")
	t.Setenv("MUSIC_SESSION_TTL", "1h")
	t.Setenv("MUSIC_PUBLIC_URL", "https://example.test/music")

	g := New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := g.Mount(http.NewServeMux(), platform.Deps{Logger: logger}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if g.LLMSPublicURL() != "https://example.test/music" {
		t.Errorf("LLMSPublicURL = %q", g.LLMSPublicURL())
	}
	resolver, ok := g.GraphQLResolver().(*musicgraph.Resolver)
	if !ok || resolver == nil {
		t.Fatalf("GraphQLResolver = %T, want *musicgraph.Resolver", g.GraphQLResolver())
	}
	if err := g.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := g.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestMount_MissingScoreDirsAreCreated(t *testing.T) {
	root := t.TempDir()
	scoresDir := filepath.Join(root, "scores")
	t.Setenv("MUSIC_SCORES_DIR", scoresDir)
	t.Setenv("MUSIC_SESSIONS_DIR", filepath.Join(root, "sessions"))
	t.Setenv("MUSIC_MAX_SESSIONS", "10")

	g := New()
	if err := g.Mount(http.NewServeMux(), platform.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	defer g.Shutdown(context.Background())
	if info, err := os.Stat(scoresDir); err != nil || !info.IsDir() {
		t.Fatalf("scores dir was not created: %v", err)
	}
}
