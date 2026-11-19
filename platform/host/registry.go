package host

import (
	"fmt"

	"github.com/wricardo/drbrain/platform"
)

// Registry is the compiled-in set of games cmd/drbrain can serve. Order is
// preserved: it decides the default enable order and `games list`.
type Registry struct {
	order  []platform.Game
	byName map[string]platform.Game
}

// NewRegistry validates the compiled-in games. Duplicate names or prefixes are
// a programming error and fail before any listener exists.
func NewRegistry(games ...platform.Game) (*Registry, error) {
	r := &Registry{byName: make(map[string]platform.Game, len(games))}
	prefixes := make(map[string]string, len(games))
	for _, g := range games {
		if g == nil {
			return nil, fmt.Errorf("registry: nil game")
		}
		name := g.Name()
		if name == "" {
			return nil, fmt.Errorf("registry: game with empty name")
		}
		if _, dup := r.byName[name]; dup {
			return nil, fmt.Errorf("registry: duplicate game %q", name)
		}
		prefix := g.Prefix()
		if prefix == "" {
			return nil, fmt.Errorf("registry: game %q has empty prefix", name)
		}
		if other, dup := prefixes[prefix]; dup {
			return nil, fmt.Errorf("registry: games %q and %q share prefix %q", other, name, prefix)
		}
		prefixes[prefix] = name
		r.byName[name] = g
		r.order = append(r.order, g)
	}
	return r, nil
}

// Games returns the compiled-in games in registration order.
func (r *Registry) Games() []platform.Game { return append([]platform.Game(nil), r.order...) }

// Names returns the game names in registration order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.order))
	for _, g := range r.order {
		names = append(names, g.Name())
	}
	return names
}

// Lookup returns the named game.
func (r *Registry) Lookup(name string) (platform.Game, bool) {
	g, ok := r.byName[name]
	return g, ok
}

// Select resolves the requested names against the registry. Empty means every
// game. An unknown or repeated name is an error, so --games typos fail before
// the listener binds.
func (r *Registry) Select(names []string) ([]platform.Game, error) {
	if len(names) == 0 {
		return r.Games(), nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]platform.Game, 0, len(names))
	for _, name := range names {
		if seen[name] {
			return nil, fmt.Errorf("game %q selected twice", name)
		}
		seen[name] = true
		g, ok := r.byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown game %q (compiled in: %v)", name, r.Names())
		}
		out = append(out, g)
	}
	return out, nil
}
