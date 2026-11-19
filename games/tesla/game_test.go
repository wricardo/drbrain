package tesla

import (
	"strings"
	"testing"
)

func TestGameMetadata(t *testing.T) {
	g := New()
	if g.Name() != "tesla" || g.Prefix() != "/tesla" {
		t.Errorf("name/prefix = %q/%q", g.Name(), g.Prefix())
	}
	want := []string{"/graphql", "/playground", "/mcp"}
	got := g.API()
	if len(got) != len(want) {
		t.Fatalf("API = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("API[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !strings.Contains(g.LLMSTemplate(), "{{.BaseURL}}/graphql") {
		t.Error("llms template does not render the base URL")
	}
}
