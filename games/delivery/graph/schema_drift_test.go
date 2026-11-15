package graph_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGeneratedTypesAreUpToDate fails when the GraphQL schema changed without
// the TypeScript client types being regenerated.
//
// The schema is the single source of truth: gqlgen generates the Go types from
// it, cmd/gentypes generates the TypeScript from it. Without this guard the
// generated file is just another artefact someone forgets to refresh, and the
// client silently starts describing shapes the server does not serve.
//
// It shells out to the generator rather than duplicating its logic, so there is
// exactly one implementation.
func TestGeneratedTypesAreUpToDate(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "cmd", "gentypes")); err != nil {
		t.Skipf("generator not found: %v", err)
	}

	cmd := exec.Command("go", "run", "./cmd/gentypes", "-check")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated API types are stale — run `make types`.\n\n%s", out)
	}
}

// TestGqlgenRegenerateIsStable guards the property that makes the generated
// resolvers safe to keep in version control: running gqlgen again must produce
// byte-identical output. If it does not, either the schema changed without a
// committed regenerate, or hand-written code drifted into the generated file
// (gqlgen moves unknown code to a warning block, which this would catch).
func TestGqlgenRegenerateIsStable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping codegen check in -short mode")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	files := []string{
		filepath.Join("graph", "generated", "generated.go"),
		filepath.Join("graph", "schema.resolvers.go"),
		filepath.Join("graph", "model", "models_gen.go"),
	}

	before := make([][]byte, len(files))
	for i, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Skipf("generated file missing: %v", err)
		}
		before[i] = b
	}

	cmd := exec.Command("go", "run", "github.com/99designs/gqlgen", "generate")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gqlgen generate failed:\n%s", out)
	}

	for i, f := range files {
		after, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		if string(before[i]) != string(after) {
			t.Fatalf("%s changed on regenerate — run `make generate` and commit the result", f)
		}
	}
}
