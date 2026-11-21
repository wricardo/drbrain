package score

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wricardo/drbrain/platform/mapstore"
)

// Catalogue is the score library: shipped pieces plus an optional
// admin-authored custom directory with edit-token sidecars.
type Catalogue struct {
	store *mapstore.Store[Piece]
}

// Entry is one catalogue row. Piece is a deep copy, never the cache.
type Entry struct {
	ID      string
	Piece   *Piece
	Builtin bool
}

// NewCatalogue opens builtinDir and customDir. A blank customDir means the
// sibling directory <builtinDir>/../scores-custom (games/music/scores-custom in
// the repo, /var/lib/drbrain/music/scores-custom in production), never a
// writable directory inside the versioned shipped-score library. An explicit
// customDir wins unchanged. Every existing .json file in both directories is
// parsed, validated and checked against its file name; the first bad file
// aborts startup, naming the file.
func NewCatalogue(builtinDir, customDir string) (*Catalogue, error) {
	if strings.TrimSpace(builtinDir) == "" {
		return nil, errors.New("music: scores dir is empty")
	}
	custom := strings.TrimSpace(customDir)
	if custom == "" {
		custom = filepath.Join(builtinDir, "..", "scores-custom")
	}
	custom, err := filepath.Abs(custom)
	if err != nil {
		return nil, fmt.Errorf("music: custom scores dir: %w", err)
	}
	if err := os.MkdirAll(builtinDir, 0o755); err != nil {
		return nil, fmt.Errorf("music: create scores dir: %w", err)
	}
	store, err := mapstore.Open(mapstore.Options[Piece]{
		BuiltinDir: builtinDir,
		CustomDir:  custom,
		Decode: func(raw []byte) (Piece, error) {
			p, err := decode(raw, MaxMeasures)
			if err != nil {
				return Piece{}, err
			}
			return *p, nil
		},
		Encode:   func(p Piece) ([]byte, error) { return Encode(&p) },
		Validate: func(p Piece) error { return Validate(&p) },
	})
	if err != nil {
		return nil, err
	}
	c := &Catalogue{store: store}
	if err := c.validateAll(builtinDir, store.CustomDir()); err != nil {
		return nil, err
	}
	return c, nil
}

// validateAll parses every score file up front. The store by design skips
// invalid entries when listing, but a shipped or custom score that cannot be
// played must abort Mount instead of quietly disappearing.
func (c *Catalogue) validateAll(dirs ...string) error {
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("music: scores dir %s: %w", dir, err)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("music: read scores dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			base := strings.TrimSuffix(e.Name(), ".json")
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if _, err := Parse(base, raw); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	return nil
}

// CustomDir is where admin-authored scores live.
func (c *Catalogue) CustomDir() string { return c.store.CustomDir() }

// IsBuiltin reports whether id is a shipped score.
func (c *Catalogue) IsBuiltin(id string) bool { return c.store.IsBuiltin(id) }

// List returns every builtin then custom score, sorted by id. Pieces are deep
// copies.
func (c *Catalogue) List() ([]Entry, error) {
	rows, err := c.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		p := row.Config
		out = append(out, Entry{ID: row.Name, Piece: p.Clone(), Builtin: row.Builtin})
	}
	return out, nil
}

// Get returns a deep copy of one score. A missing score wraps
// mapstore.ErrNotFound; the service layer maps errors to GraphQL codes.
func (c *Catalogue) Get(id string) (*Piece, error) {
	row, err := c.store.Get(id)
	if err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return nil, fmt.Errorf("%w: score %q", mapstore.ErrNotFound, id)
		}
		return nil, fmt.Errorf("score %q: %w", id, err)
	}
	return row.Config.Clone(), nil
}
