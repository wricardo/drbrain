package mazes

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/platform"
)

func TestGameMetadata(t *testing.T) {
	g := New()
	if g.Name() != "mazes" {
		t.Errorf("Name = %q", g.Name())
	}
	if g.Prefix() != "/mazes" {
		t.Errorf("Prefix = %q", g.Prefix())
	}
	// GraphQL moved to the platform root; mazes exposes no game-local API.
	if got := g.API(); len(got) != 0 {
		t.Errorf("API = %v, want none", got)
	}
	if !strings.Contains(g.LLMSTemplate(), "{{.GraphQLURL}}") {
		t.Error("llms template does not render the GraphQL URL")
	}
}

func TestLoadEnvDefaults(t *testing.T) {
	for _, key := range []string{"MAZES_MAPS_DIR", "MAZES_SOLUTIONS_DIR", "MAZES_PUBLIC_URL", "MAZES_DEBUG", "MAZES_MAX_SESSIONS", "MAZES_MAX_USER_MAPS"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.MapsDir != "maps" || e.SolutionsDir != "solutions" {
		t.Errorf("dirs = %q/%q", e.MapsDir, e.SolutionsDir)
	}
	if e.MaxSessions != 1000 {
		t.Errorf("MaxSessions = %d, want 1000", e.MaxSessions)
	}
	if e.MaxUserMaps != 200 {
		t.Errorf("MaxUserMaps = %d, want 200", e.MaxUserMaps)
	}
	if e.Debug {
		t.Error("Debug = true by default")
	}
}

func TestLoadEnvPrefixed(t *testing.T) {
	t.Setenv("MAZES_MAPS_DIR", "custom-maps")
	t.Setenv("MAZES_MAX_SESSIONS", "7")
	t.Setenv("MAZES_MAX_USER_MAPS", "9")
	t.Setenv("MAZES_PUBLIC_URL", "https://example.test")
	t.Setenv("MAZES_DEBUG", "true")
	e, err := loadEnv()
	if err != nil {
		t.Fatal(err)
	}
	if e.MapsDir != "custom-maps" || e.MaxSessions != 7 || e.MaxUserMaps != 9 || e.PublicURL != "https://example.test" || !e.Debug {
		t.Errorf("env = %+v", e)
	}
}

func TestMountRequiresDatabase(t *testing.T) {
	t.Setenv("MAZES_MAPS_DIR", "maps")
	if err := New().Mount(http.NewServeMux(), platform.Deps{}); err == nil {
		t.Fatal("Mount without a database succeeded")
	}
}
