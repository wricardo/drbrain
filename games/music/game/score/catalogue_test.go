package score

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain/platform/mapstore"
)

func scoreJSON(id string) []byte {
	return fmt.Appendf(nil, `{
  "version": 1,
  "id": %q,
  "title": %q,
  "composer": "Anonymous",
  "tempoBPM": 80,
  "timeSig": "4/4",
  "key": "C major",
  "clef": "treble",
  "measures": [{"len": 480, "notes": [{"start": 0, "dur": 480, "pitch": 71}]}]
}`, id, id)
}

func writeScoreFile(t *testing.T, dir, base string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, base+".json"), raw, 0o644); err != nil {
		t.Fatalf("write %s.json: %v", base, err)
	}
}

func newScoreDirs(t *testing.T) (builtin, custom string) {
	t.Helper()
	root := t.TempDir()
	builtin = filepath.Join(root, "scores")
	custom = filepath.Join(root, "custom-scores")
	for _, dir := range []string{builtin, custom} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return builtin, custom
}

func TestCatalogue_ListsAndGetsBuiltinAndCustom(t *testing.T) {
	builtin, custom := newScoreDirs(t)
	writeScoreFile(t, builtin, "twinkle", []byte(validScoreJSON))
	writeScoreFile(t, custom, "ode", scoreJSON("ode"))

	c, err := NewCatalogue(builtin, custom)
	if err != nil {
		t.Fatalf("NewCatalogue: %v", err)
	}

	entries, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List = %d entries, want 2", len(entries))
	}
	if entries[0].ID != "ode" || entries[1].ID != "twinkle" {
		t.Fatalf("List order = %q, %q", entries[0].ID, entries[1].ID)
	}
	if entries[0].Builtin || !entries[1].Builtin {
		t.Fatalf("builtin flags = %v, %v", entries[0].Builtin, entries[1].Builtin)
	}

	if !c.IsBuiltin("twinkle") || c.IsBuiltin("ode") {
		t.Fatalf("IsBuiltin = %v/%v", c.IsBuiltin("twinkle"), c.IsBuiltin("ode"))
	}
	if c.CustomDir() != custom {
		t.Fatalf("CustomDir = %q, want %q", c.CustomDir(), custom)
	}

	got, err := c.Get("ode")
	if err != nil {
		t.Fatalf("Get(ode): %v", err)
	}
	if got.ID != "ode" || got.Measures[0].ID != "m001" {
		t.Fatalf("Get(ode) = %+v", got)
	}
	if got.Axis != Axis {
		t.Fatalf("axis = %d, want %d", got.Axis, Axis)
	}
}

func TestCatalogue_GetReturnsDeepCopy(t *testing.T) {
	builtin, custom := newScoreDirs(t)
	writeScoreFile(t, builtin, "twinkle", []byte(validScoreJSON))
	c, err := NewCatalogue(builtin, custom)
	if err != nil {
		t.Fatalf("NewCatalogue: %v", err)
	}

	first, err := c.Get("twinkle")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	first.Measures[0].Notes[0].Pitch = MinPitch
	first.Measures[0].Len = 1

	second, err := c.Get("twinkle")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if second.Measures[0].Len != 480 || second.Measures[0].Notes[0].Pitch == MinPitch {
		t.Fatal("Get returned the catalogue's own piece")
	}
}

func TestCatalogue_GetMissingWrapsErrNotFound(t *testing.T) {
	builtin, custom := newScoreDirs(t)
	writeScoreFile(t, builtin, "twinkle", []byte(validScoreJSON))
	c, err := NewCatalogue(builtin, custom)
	if err != nil {
		t.Fatalf("NewCatalogue: %v", err)
	}
	if _, err := c.Get("missing"); !errors.Is(err, mapstore.ErrNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestCatalogue_BlankCustomDirDefaultsToSibling(t *testing.T) {
	root := t.TempDir()
	builtin := filepath.Join(root, "scores")
	if err := os.MkdirAll(builtin, 0o755); err != nil {
		t.Fatalf("mkdir builtin: %v", err)
	}
	writeScoreFile(t, builtin, "twinkle", []byte(validScoreJSON))

	c, err := NewCatalogue(builtin, "")
	if err != nil {
		t.Fatalf("NewCatalogue: %v", err)
	}
	custom := c.CustomDir()
	if filepath.Base(custom) != "scores-custom" {
		t.Fatalf("CustomDir = %q, want a scores-custom directory", custom)
	}
	if !filepath.IsAbs(custom) {
		t.Fatalf("CustomDir = %q, want an absolute path", custom)
	}
	if filepath.Dir(custom) != filepath.Dir(builtin) {
		t.Fatalf("CustomDir = %q is not a sibling of %q", custom, builtin)
	}
	if rel, err := filepath.Rel(builtin, custom); err == nil && rel != ".." && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		t.Fatalf("CustomDir %q is inside the shipped library %q", custom, builtin)
	}
	if info, err := os.Stat(custom); err != nil || !info.IsDir() {
		t.Fatalf("custom dir was not created: %v", err)
	}

	// A score written through the custom path is a custom entry and never
	// lands in (or grows) the shipped-score directory.
	writeScoreFile(t, custom, "ode", scoreJSON("ode"))
	entries, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List = %d entries, want 2", len(entries))
	}
	for _, e := range entries {
		switch e.ID {
		case "twinkle":
			if !e.Builtin {
				t.Fatal("twinkle is not marked builtin")
			}
		case "ode":
			if e.Builtin {
				t.Fatal("custom ode was reported as builtin")
			}
		default:
			t.Fatalf("unexpected entry %q", e.ID)
		}
	}
	builtinFiles, err := os.ReadDir(builtin)
	if err != nil {
		t.Fatalf("read builtin dir: %v", err)
	}
	jsonCount := 0
	for _, f := range builtinFiles {
		if strings.HasSuffix(f.Name(), ".json") {
			jsonCount++
		}
	}
	if jsonCount != 1 {
		t.Fatalf("builtin dir has %d .json files, want 1 (custom scores must not land there)", jsonCount)
	}
}

func TestNewCatalogue_InvalidFileNamesIt(t *testing.T) {
	cases := []struct {
		name string
		file string
		raw  string
	}{
		{"broken json", "broken", `{"version": 1,`},
		{"unknown field", "broken", strings.Replace(validScoreJSON, `"clef": "treble",`, `"clef": "treble", "axis": 71,`, 1)},
		{"invalid content", "broken", `{"version": 1, "id": "broken"}`},
		{"id mismatch", "wrong", string(scoreJSON("right"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builtin, custom := newScoreDirs(t)
			writeScoreFile(t, builtin, tc.file, []byte(tc.raw))
			_, err := NewCatalogue(builtin, custom)
			if err == nil {
				t.Fatal("NewCatalogue accepted an invalid score")
			}
			if !strings.Contains(err.Error(), tc.file+".json") {
				t.Fatalf("error %q should name %s.json", err, tc.file)
			}
		})
	}
}

func TestNewCatalogue_InvalidCustomFileNamesIt(t *testing.T) {
	builtin, custom := newScoreDirs(t)
	writeScoreFile(t, builtin, "twinkle", []byte(validScoreJSON))
	writeScoreFile(t, custom, "bad", []byte(`{"version": 1, "id": "bad"}`))

	_, err := NewCatalogue(builtin, custom)
	if err == nil {
		t.Fatal("NewCatalogue accepted an invalid custom score")
	}
	if !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("error %q should name bad.json", err)
	}
}

func TestCatalogue_RejectsPieceOverTheCapAtStartup(t *testing.T) {
	builtin, custom := newScoreDirs(t)
	bar := `{"len": 480, "notes": [{"start": 0, "dur": 480, "pitch": 71}]}`
	writeScoreFile(t, builtin, "long", fmt.Appendf(nil, `{
  "version": 1,
  "id": "long",
  "title": "long",
  "composer": "Anonymous",
  "tempoBPM": 80,
  "timeSig": "4/4",
  "key": "C major",
  "clef": "treble",
  "measures": [%s]
}`, strings.TrimSuffix(strings.Repeat(bar+",", 7), ",")))

	_, err := NewCatalogue(builtin, custom)
	if err == nil {
		t.Fatal("NewCatalogue accepted a piece over the cap")
	}
	msg := err.Error()
	for _, want := range []string{"long.json", "7 measures", "caps pieces at 6"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q", msg, want)
		}
	}
}
