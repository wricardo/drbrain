// Package mapstore owns the map rules every game reimplements: builtin vs
// custom directories, duplicate refusal, admin-only builtin writes, and edit
// tokens for custom maps. The game supplies decode/encode and a validator; the
// store never sees engine types and never authenticates.
package mapstore

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Errors every game maps to its own public error vocabulary.
var (
	ErrNotFound          = errors.New("mapstore: map not found")
	ErrExists            = errors.New("mapstore: map already exists")
	ErrInvalid           = errors.New("mapstore: invalid map")
	ErrReadOnly          = errors.New("mapstore: built-in map is read-only")
	ErrEditTokenRequired = errors.New("mapstore: edit token required")
	ErrInvalidToken      = errors.New("mapstore: invalid edit token")
)

// editTokenSuffix is the custom-map sidecar, unchanged from tesla's
// implementation so existing tokens keep working.
const editTokenSuffix = ".edit-token"

type tokenRecord struct {
	TokenHash string `json:"token_hash"`
}

// Map is one entry in the catalogue.
type Map[T any] struct {
	Name    string
	Config  T
	Builtin bool
}

// Options configures a store. BuiltinDir is required.
type Options[T any] struct {
	BuiltinDir string
	// CustomDir holds user-created maps; empty means <BuiltinDir>/custom.
	CustomDir string
	Decode    func([]byte) (T, error)
	Encode    func(T) ([]byte, error)
	// Validate is the game's map validator. It runs before every write.
	Validate func(T) error
	// NoEditTokens disables token sidecars for games whose maps are
	// admin-managed and single-directory (mazes). Create then returns "".
	NoEditTokens bool
}

// Store is a map catalogue with builtin and custom directories.
type Store[T any] struct {
	builtinDir   string
	customDir    string
	decode       func([]byte) (T, error)
	encode       func(T) ([]byte, error)
	validate     func(T) error
	noEditTokens bool

	mu    sync.RWMutex
	cache map[string]T
}

// Open validates the directories and creates them when missing.
func Open[T any](opts Options[T]) (*Store[T], error) {
	if strings.TrimSpace(opts.BuiltinDir) == "" {
		return nil, fmt.Errorf("mapstore: builtin dir is empty")
	}
	if opts.Decode == nil || opts.Encode == nil || opts.Validate == nil {
		return nil, fmt.Errorf("mapstore: Decode, Encode and Validate are required")
	}
	custom := strings.TrimSpace(opts.CustomDir)
	if custom == "" {
		custom = filepath.Join(opts.BuiltinDir, "custom")
	}
	if err := os.MkdirAll(custom, 0o755); err != nil {
		return nil, fmt.Errorf("mapstore: create custom dir: %w", err)
	}
	return &Store[T]{
		builtinDir:   opts.BuiltinDir,
		customDir:    custom,
		decode:       opts.Decode,
		encode:       opts.Encode,
		validate:     opts.Validate,
		noEditTokens: opts.NoEditTokens,
		cache:        make(map[string]T),
	}, nil
}

// CustomDir is where user-created maps live.
func (s *Store[T]) CustomDir() string { return s.customDir }

// Refresh drops the parsed-config cache, so the next read sees files changed
// outside this process.
func (s *Store[T]) Refresh() {
	s.mu.Lock()
	s.cache = make(map[string]T)
	s.mu.Unlock()
}

// List returns builtin maps then custom maps, sorted by name.
func (s *Store[T]) List() ([]Map[T], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Map[T]
	seen := make(map[string]bool)
	for _, group := range []struct {
		dir     string
		builtin bool
	}{{s.builtinDir, true}, {s.customDir, false}} {
		names, err := mapNames(group.dir)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if seen[name] {
				// A custom file can never shadow a builtin of the same name.
				continue
			}
			cfg, err := s.loadLocked(name, group.dir)
			if errors.Is(err, ErrInvalid) {
				// A corrupt or invalid file is omitted from the catalogue; Get
				// still reports the exact problem when asked for it.
				continue
			}
			if err != nil {
				return nil, err
			}
			seen[name] = true
			out = append(out, Map[T]{Name: name, Config: cfg, Builtin: group.builtin})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns a map, preferring the builtin copy.
func (s *Store[T]) Get(name string) (Map[T], error) {
	base, err := cleanName(name)
	if err != nil {
		return Map[T]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) {
		cfg, err := s.loadLocked(base, s.builtinDir)
		return Map[T]{Name: base, Config: cfg, Builtin: true}, err
	}
	if fileExists(filepath.Join(s.customDir, base+".json")) {
		cfg, err := s.loadLocked(base, s.customDir)
		return Map[T]{Name: base, Config: cfg, Builtin: false}, err
	}
	return Map[T]{}, fmt.Errorf("%w: %q", ErrNotFound, base)
}

// IsBuiltin reports whether name is a shipped map.
func (s *Store[T]) IsBuiltin(name string) bool {
	base, err := cleanName(name)
	if err != nil {
		return false
	}
	return fileExists(filepath.Join(s.builtinDir, base+".json"))
}

// Create stores a new custom map and returns its edit token. It never
// overwrites: a builtin or custom map with the name yields ErrExists.
func (s *Store[T]) Create(name string, cfg T) (string, error) {
	base, err := cleanName(name)
	if err != nil {
		return "", err
	}
	if err := s.validate(cfg); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) || fileExists(filepath.Join(s.customDir, base+".json")) {
		return "", fmt.Errorf("%w: %q", ErrExists, base)
	}
	token := ""
	if !s.noEditTokens {
		var err error
		token, err = newEditToken()
		if err != nil {
			return "", err
		}
		if err := s.writeTokenLocked(base, token); err != nil {
			return "", err
		}
	}
	if err := s.writeLocked(base, cfg, s.customDir); err != nil {
		if token != "" {
			_ = os.Remove(s.tokenPath(base))
		}
		return "", err
	}
	return token, nil
}

// Save overwrites an existing custom map. admin allows overwriting a builtin.
// A custom map's token must have been verified by the caller first.
func (s *Store[T]) Save(name string, cfg T, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case fileExists(filepath.Join(s.builtinDir, base+".json")):
		return s.saveBuiltinLocked(base, cfg, admin)
	case fileExists(filepath.Join(s.customDir, base+".json")):
		if err := s.validate(cfg); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return s.writeLocked(base, cfg, s.customDir)
	default:
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
}

// SaveBuiltin overwrites a shipped map. admin must be true; the transport
// authenticates before passing it.
func (s *Store[T]) SaveBuiltin(name string, cfg T, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveBuiltinLocked(base, cfg, admin)
}

func (s *Store[T]) saveBuiltinLocked(base string, cfg T, admin bool) error {
	if !admin {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if !fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if err := s.validate(cfg); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.writeLocked(base, cfg, s.builtinDir)
}

// Delete removes a map. Builtins require admin; custom maps need a verified
// token (the caller checks it) and are deleted with their token sidecar.
func (s *Store[T]) Delete(name string, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return s.deleteBuiltinLocked(base, admin)
	}
	if fileExists(filepath.Join(s.customDir, base+".json")) {
		if err := removeMap(s.customDir, base); err != nil {
			return err
		}
		delete(s.cache, base)
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNotFound, base)
}

// DeleteBuiltin removes a shipped map. admin must be true.
func (s *Store[T]) DeleteBuiltin(name string, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteBuiltinLocked(base, admin)
}

func (s *Store[T]) deleteBuiltinLocked(base string, admin bool) error {
	if !admin {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if !fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if err := removeMap(s.builtinDir, base); err != nil {
		return err
	}
	delete(s.cache, base)
	return nil
}

// VerifyEditToken checks a custom map's token. Builtins always fail: use admin
// credentials for them.
func (s *Store[T]) VerifyEditToken(name, token string) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if !fileExists(filepath.Join(s.customDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if s.noEditTokens || strings.TrimSpace(token) == "" {
		return ErrEditTokenRequired
	}
	raw, err := os.ReadFile(s.tokenPath(base))
	if err != nil {
		// A custom map predating tokens requires admin instead.
		return ErrEditTokenRequired
	}
	var rec tokenRecord
	if err := json.Unmarshal(raw, &rec); err != nil || rec.TokenHash == "" {
		return ErrEditTokenRequired
	}
	got := hashToken(token)
	if subtle.ConstantTimeCompare([]byte(got), []byte(rec.TokenHash)) != 1 {
		return ErrInvalidToken
	}
	return nil
}

// ---- files ----------------------------------------------------------------

func (s *Store[T]) loadLocked(name, dir string) (T, error) {
	if cfg, ok := s.cache[name]; ok {
		return cfg, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		var zero T
		return zero, fmt.Errorf("mapstore: read %s: %w", name, err)
	}
	cfg, err := s.decode(raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("mapstore: decode %s: %w", name, err)
	}
	if err := s.validate(cfg); err != nil {
		var zero T
		return zero, fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
	}
	s.cache[name] = cfg
	return cfg, nil
}

func (s *Store[T]) writeLocked(name string, cfg T, dir string) error {
	raw, err := s.encode(cfg)
	if err != nil {
		return fmt.Errorf("mapstore: encode %s: %w", name, err)
	}
	if err := writeAtomic(dir, filepath.Join(dir, name+".json"), raw); err != nil {
		return fmt.Errorf("mapstore: write %s: %w", name, err)
	}
	s.cache[name] = cfg
	return nil
}

func (s *Store[T]) writeTokenLocked(name, token string) error {
	rec := tokenRecord{TokenHash: hashToken(token)}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("mapstore: encode token for %s: %w", name, err)
	}
	if err := os.WriteFile(s.tokenPath(name), raw, 0o600); err != nil {
		return fmt.Errorf("mapstore: write token for %s: %w", name, err)
	}
	return nil
}

func (s *Store[T]) tokenPath(name string) string {
	return filepath.Join(s.customDir, name+".json"+editTokenSuffix)
}

func mapNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("mapstore: list %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if _, err := cleanName(name); err == nil {
			names = append(names, name)
		}
	}
	return names, nil
}

func removeMap(dir, name string) error {
	if err := os.Remove(filepath.Join(dir, name+".json")); err != nil {
		return fmt.Errorf("mapstore: delete %s: %w", name, err)
	}
	// Best effort: the token sidecar is meaningless without the map.
	_ = os.Remove(filepath.Join(dir, name+".json"+editTokenSuffix))
	return nil
}

func writeAtomic(dir, path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// cleanName accepts "foo", "foo.json" and surrounding spaces; it rejects
// traversal so a name is always a safe file base.
func cleanName(name string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(name)), ".json")
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("%w: name %q", ErrInvalid, name)
	}
	return base, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func newEditToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mapstore: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
