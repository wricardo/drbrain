// Command merge-schema rebuilds graph/schema.graphqls and graph/gqlgen.yml from
// the derived per-game schemas and compares them with the canonical files. It
// is the round-trip verifier for cmd/split-schema; prefer -check in CI.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wricardo/drbrain/graph/merge"
)

func main() {
	out := flag.String("out", "", "schema output file (default: <repo>/graph/schema.graphqls)")
	cfgOut := flag.String("config-out", "", "gqlgen config output file (default: <repo>/graph/gqlgen.yml)")
	check := flag.Bool("check", false, "verify the outputs are current instead of writing")
	flag.Parse()

	root, err := merge.RepoRoot()
	if err != nil {
		fail(err)
	}
	schemaPath := *out
	if schemaPath == "" {
		schemaPath = filepath.Join(root, merge.SchemaPath)
	}
	configPath := *cfgOut
	if configPath == "" {
		configPath = filepath.Join(root, "graph", "gqlgen.yml")
	}

	sdl, err := merge.BuildFrom(root)
	if err != nil {
		fail(err)
	}
	cfg, err := merge.GQLGenConfig(root)
	if err != nil {
		fail(err)
	}

	if *check {
		checkFile(schemaPath, sdl)
		checkFile(configPath, cfg)
		fmt.Println("generated files are current")
		return
	}
	writeFile(schemaPath, sdl)
	writeFile(configPath, cfg)
}

func checkFile(path string, want []byte) {
	got, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	if !bytes.Equal(got, want) {
		fail(fmt.Errorf("%s is stale: run go run ./cmd/merge-schema", path))
	}
}

func writeFile(path string, body []byte) {
	if err := os.WriteFile(path, body, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s\n", path)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "merge-schema: %v\n", err)
	os.Exit(1)
}
