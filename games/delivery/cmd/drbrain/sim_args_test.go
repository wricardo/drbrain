package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliCity is the shipped map, resolved from the package directory so these
// tests run from anywhere.
func cliCity(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("shipped city not readable: %v", err)
	}
	return path
}

// TestSimRejectsBadArguments is the regression test for three invocations that
// used to crash or, worse, quietly produce no work at all:
//
//	sim -ticks            panicked with an index out of range
//	sim -every 0          panicked with a divide by zero in the tick loop
//	sim -ticks abc        ran 0 ticks, exited 0, and printed a table
//
// The last one is why this matters beyond tidiness: the README's pacing table is
// cited as evidence, so a typo in a recorded command must fail loudly rather
// than emit a header and a row that look like a real run.
func TestSimRejectsBadArguments(t *testing.T) {
	city := cliCity(t)
	cases := []struct {
		name string
		args []string
	}{
		{"missing value", []string{"-f", city, "-ticks"}},
		{"unparseable ticks", []string{"-f", city, "-ticks", "abc"}},
		{"zero ticks", []string{"-f", city, "-ticks", "0"}},
		{"negative ticks", []string{"-f", city, "-ticks", "-5"}},
		{"zero every", []string{"-f", city, "-every", "0", "-ticks", "5"}},
		{"negative every", []string{"-f", city, "-every", "-3", "-ticks", "5"}},
		{"unknown flag", []string{"-f", city, "-nope", "1"}},
		{"unparseable seed", []string{"-f", city, "-seed", "zzz", "-ticks", "1"}},
		{"stray argument", []string{"-f", city, "-ticks", "5", "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cmdSim(tc.args); got != 2 {
				t.Fatalf("cmdSim(%v) = %d, want 2 (usage error)", tc.args, got)
			}
		})
	}
}

// TestSimAcceptsGoodArguments is the other half: validation must not reject a
// normal run, or the harness becomes unusable and the fix is worse than the bug.
func TestSimAcceptsGoodArguments(t *testing.T) {
	city := cliCity(t)
	if got := cmdSim([]string{"-f", city, "-ticks", "3", "-every", "1"}); got != 0 {
		t.Fatalf("cmdSim with valid arguments = %d, want 0", got)
	}
}

// TestSimUsageMentionsEveryFlag guards against a flag being added to the parse
// but not to the help text, which is how `-f` silently became undiscoverable
// when this file was hand-rolled.
func TestSimUsageMentionsEveryFlag(t *testing.T) {
	fs := newSimFlagSet(new(simOptions))
	var b strings.Builder
	fs.SetOutput(&b)
	fs.Usage()
	usage := b.String()
	for _, want := range []string{"-f", "-ticks", "-every", "-seed"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage text is missing %s:\n%s", want, usage)
		}
	}
}
