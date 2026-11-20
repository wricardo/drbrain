package main

import (
	"os"
	"testing"

	"github.com/wricardo/drbrain/graph/merge"
)

func TestDerivedSchemasAreCurrent(t *testing.T) {
	root, err := merge.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := splitAll(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range outputs {
		got, err := os.ReadFile(out.path)
		if err != nil {
			t.Fatalf("read %s: %v (run go run ./cmd/split-schema)", out.path, err)
		}
		if string(got) != out.sdl {
			t.Errorf("%s is stale: run go run ./cmd/split-schema", out.path)
		}
	}
}
