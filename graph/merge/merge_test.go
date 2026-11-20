package merge

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestSchemaIsCurrent(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, err := BuildFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, SchemaPath))
	if err != nil {
		t.Fatalf("read %s: %v (run go run ./cmd/merge-schema)", SchemaPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: run go run ./cmd/merge-schema", SchemaPath)
	}

	cfg, err := GQLGenConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	gotCfg, err := os.ReadFile(filepath.Join(root, "graph", "gqlgen.yml"))
	if err != nil {
		t.Fatalf("read graph/gqlgen.yml: %v", err)
	}
	if !bytes.Equal(gotCfg, cfg) {
		t.Fatalf("graph/gqlgen.yml is stale: run go run ./cmd/merge-schema")
	}
}

func TestMergedSchemaShape(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	sdl, err := BuildFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: SchemaPath, Input: string(sdl)})
	if err != nil {
		t.Fatalf("merged schema is invalid: %v", err)
	}

	names := func(def *ast.Definition) []string {
		out := make([]string, 0, len(def.Fields))
		for _, f := range def.Fields {
			if strings.HasPrefix(f.Name, "__") {
				continue // gqlparser adds the introspection meta-fields
			}
			out = append(out, f.Name)
		}
		return out
	}
	if got, want := names(schema.Query), []string{"delivery", "mazes", "motorProgramming", "tesla"}; !slices.Equal(got, want) {
		t.Fatalf("Query fields = %v, want %v", got, want)
	}
	if got, want := names(schema.Mutation), []string{"delivery", "mazes", "motorProgramming", "tesla"}; !slices.Equal(got, want) {
		t.Fatalf("Mutation fields = %v, want %v", got, want)
	}
	wantSubs := []string{
		"deliverySession",
		"mazesSessionUpdated",
		"motorProgrammingSessionUpdated",
		"teslaSessionUpdated",
		"teslaLobbyUpdated",
	}
	if got := names(schema.Subscription); !slices.Equal(got, wantSubs) {
		t.Fatalf("Subscription fields = %v, want %v", got, wantSubs)
	}

	for _, g := range Games {
		for _, typ := range []string{"Query", "Mutation"} {
			name := g.Name + "_" + typ
			if schema.Types[name] == nil {
				t.Errorf("missing namespace type %s", name)
			}
		}
	}

	// Prefixing must keep the four dialects apart. A bare Session type would
	// mean the merge silently collapsed two games.
	for _, bare := range []string{"Session", "Map", "Position", "Direction", "Status"} {
		if schema.Types[bare] != nil {
			t.Errorf("unprefixed type %q leaked into the merged schema", bare)
		}
	}
	if schema.Types["Time"] == nil {
		t.Error("shared Time scalar is missing")
	}
}
