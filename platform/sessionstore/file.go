package sessionstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SummaryFunc derives a session's Meta from its payload. Games supply it so
// List can report saves written by the old per-game format (a raw payload file
// with no platform envelope).
type SummaryFunc func(payload []byte) (Meta, error)

// Options configures a store. Exactly one of Dir and Pool selects the backend.
type Options struct {
	// Dir is the file backend's sessions directory.
	Dir string
	// Pool selects the postgres backend; the platform_sessions table is
	// created if absent.
	Pool *pgxpool.Pool
	// MaxSessions bounds stored sessions; <= 0 means unlimited.
	MaxSessions int
	// Evict replaces the oldest LastActionAt entry instead of returning
	// ErrLimit. Games whose manager evicts (delivery) set it; games that reject
	// (mazes, motor) leave it false.
	Evict bool
	// Summary derives Meta for legacy payload files. Optional.
	Summary SummaryFunc
}

// The payload file keeps the game's own format, so old saves and new saves are
// byte-identical and rollback is a file copy. Meta lives in a sidecar:
// <id>.json is the payload, <id>.meta.json is the platform Meta.
const metaSuffix = ".meta.json"

type entry struct {
	meta    Meta
	payload []byte
}

type store struct {
	mu      sync.Mutex
	dir     string
	pool    *pgxpool.Pool
	max     int
	evict   bool
	summary SummaryFunc
	mem     map[string]entry
}

// Open returns a store on the configured backend.
func Open(opts Options) (Store, error) {
	if (opts.Dir == "") == (opts.Pool == nil) {
		return nil, fmt.Errorf("sessionstore: exactly one of Dir or Pool must be set")
	}
	s := &store{
		dir:     opts.Dir,
		pool:    opts.Pool,
		max:     opts.MaxSessions,
		evict:   opts.Evict,
		summary: opts.Summary,
		mem:     make(map[string]entry),
	}
	if s.pool != nil {
		if err := s.initPostgres(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionstore: create dir: %w", err)
	}
	return s, nil
}

// Put registers a live entry without writing it.
func (s *store) Put(meta Meta, payload []byte) error {
	if err := validID(meta.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mem[meta.ID]; !ok {
		if err := s.roomLocked(); err != nil {
			return err
		}
	}
	meta.Loaded = true
	s.mem[meta.ID] = entry{meta: meta, payload: payload}
	return nil
}

// Get returns a loaded entry.
func (s *store) Get(id string) (Meta, []byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[id]
	return e.meta, e.payload, ok
}

// Count is the number of loaded entries.
func (s *store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mem)
}

// Delete removes the entry everywhere.
func (s *store) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, inMem := s.mem[id]
	delete(s.mem, id)
	var disk bool
	var err error
	if s.pool != nil {
		disk, err = s.pgDelete(id)
	} else {
		disk, err = s.fileDelete(id)
	}
	if err != nil {
		return err
	}
	if !inMem && !disk {
		return ErrNotFound
	}
	return nil
}

// List merges durable saves with loaded-in-memory entries, newest first.
func (s *store) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := make(map[string]Meta)
	var err error
	if s.pool != nil {
		err = s.pgList(byID)
	} else {
		err = s.fileList(byID)
	}
	if err != nil {
		return nil, err
	}
	for id, e := range s.mem {
		m := e.meta
		m.Loaded = true
		byID[id] = m
	}
	out := make([]Meta, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Persist writes the payload, then marks the entry loaded.
func (s *store) Persist(meta Meta, payload []byte) error {
	if err := validID(meta.ID); err != nil {
		return err
	}
	if len(payload) == 0 || !json.Valid(payload) {
		return fmt.Errorf("%w: payload for %s is not JSON", ErrInvalid, meta.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mem[meta.ID]; !ok {
		if err := s.roomLocked(); err != nil {
			return err
		}
	}
	if s.pool != nil {
		if err := s.pgPersist(meta, payload); err != nil {
			return err
		}
	} else if err := s.filePersist(meta, payload); err != nil {
		return err
	}
	meta.Loaded = true
	s.mem[meta.ID] = entry{meta: meta, payload: payload}
	return nil
}

// Load returns an entry, reading durable storage when it is not in memory.
func (s *store) Load(id string) (Meta, []byte, error) {
	if err := validID(id); err != nil {
		return Meta{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Always read durable storage: another process (or the same one via a
	// separate adapter) may have written a newer payload.
	var (
		meta    Meta
		payload []byte
		err     error
	)
	if s.pool != nil {
		meta, payload, err = s.pgLoad(id)
	} else {
		meta, payload, err = s.fileLoad(id)
	}
	if err != nil {
		return Meta{}, nil, err
	}
	meta.Loaded = true
	s.mem[id] = entry{meta: meta, payload: payload}
	return meta, payload, nil
}

// roomLocked enforces the limit, evicting the least recently active entry when
// the store is configured to evict.
func (s *store) roomLocked() error {
	if s.max <= 0 || len(s.mem) < s.max {
		return nil
	}
	if !s.evict {
		return ErrLimit
	}
	oldest := ""
	var oldestAt int64
	for id, e := range s.mem {
		t := e.meta.LastActionAt.UnixNano()
		if oldest == "" || t < oldestAt {
			oldest, oldestAt = id, t
		}
	}
	if oldest == "" {
		return ErrLimit
	}
	delete(s.mem, oldest)
	if s.pool != nil {
		_, _ = s.pgDelete(oldest)
	} else {
		_, _ = s.fileDelete(oldest)
	}
	return nil
}

// ---- file backend ----------------------------------------------------------

func (s *store) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *store) filePersist(meta Meta, payload []byte) error {
	if err := writeAtomic(s.dir, s.path(meta.ID), payload); err != nil {
		return fmt.Errorf("sessionstore: persist %s: %w", meta.ID, err)
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sessionstore: encode meta %s: %w", meta.ID, err)
	}
	if err := writeAtomic(s.dir, s.metaPath(meta.ID), metaJSON); err != nil {
		return fmt.Errorf("sessionstore: persist meta %s: %w", meta.ID, err)
	}
	return nil
}

// writeAtomic writes to a temp file in dir, fsyncs, then renames into place.
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
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func (s *store) metaPath(id string) string { return filepath.Join(s.dir, id+metaSuffix) }

func (s *store) fileLoad(id string) (Meta, []byte, error) {
	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Meta{}, nil, ErrNotFound
		}
		return Meta{}, nil, fmt.Errorf("sessionstore: read %s: %w", id, err)
	}
	return s.decode(id, raw)
}

// decode reads the payload and its Meta. A payload written before the
// platform sidecar existed falls back to the game's Summary.
func (s *store) decode(id string, raw []byte) (Meta, []byte, error) {
	if metaRaw, err := os.ReadFile(s.metaPath(id)); err == nil {
		var meta Meta
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			return Meta{}, nil, fmt.Errorf("sessionstore: decode meta %s: %w", id, err)
		}
		meta.ID = id
		return meta, raw, nil
	} else if !os.IsNotExist(err) {
		return Meta{}, nil, fmt.Errorf("sessionstore: read meta %s: %w", id, err)
	}
	if s.summary == nil {
		return Meta{}, nil, fmt.Errorf("%w: %s has no meta sidecar and no Summary is configured", ErrInvalid, id)
	}
	meta, err := s.summary(raw)
	if err != nil {
		return Meta{}, nil, fmt.Errorf("sessionstore: legacy %s: %w", id, err)
	}
	meta.ID = id
	return meta, raw, nil
}

func (s *store) fileDelete(id string) (bool, error) {
	_ = os.Remove(s.metaPath(id))
	err := os.Remove(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("sessionstore: remove %s: %w", id, err)
	}
	if err := syncDir(s.dir); err != nil {
		return true, err
	}
	return true, nil
}

func (s *store) fileList(dst map[string]Meta) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("sessionstore: list: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), metaSuffix) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if validID(id) != nil {
			continue
		}
		// A corrupt or unreadable save is still a known id: callers load it and
		// decide how to report the error, instead of the whole list failing.
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			dst[id] = Meta{ID: id, Loaded: false}
			continue
		}
		meta, _, err := s.decode(id, raw)
		if err != nil {
			meta = Meta{ID: id}
		}
		meta.Loaded = false
		dst[id] = meta
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// validID keeps ids safe as file names: no separators, no traversal, bounded.
func validID(id string) error {
	if id == "" || len(id) > 128 || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return fmt.Errorf("%w: id %q", ErrInvalid, id)
	}
	return nil
}
