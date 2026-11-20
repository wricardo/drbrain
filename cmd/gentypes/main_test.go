package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain/graph/merge"
)

func TestTypesAreCurrent(t *testing.T) {
	root, err := merge.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, err := generate(filepath.Join(root, merge.SchemaPath))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "web", "src", "lib", "graphql-types.ts"))
	if err != nil {
		t.Fatalf("read generated types: %v (run go run ./cmd/gentypes)", err)
	}
	if string(got) != want {
		t.Fatal("web/src/lib/graphql-types.ts is stale: run go run ./cmd/gentypes")
	}
}
