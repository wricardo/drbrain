package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const queryTimeout = 10 * time.Second

// store is the PostgreSQL backend. mem holds this process's live entries; the
// platform_sessions table holds every persisted one.
type store struct {
	mu    sync.Mutex
	pool  *pgxpool.Pool
	game  string
	max   int
	evict bool
	mem   map[string]entry
}

var _ Store = (*store)(nil)

// owns reports whether meta belongs to this store's game. An unscoped store
// (game "") owns every row; a scoped store never sees rows of other games.
func (s *store) owns(meta Meta) bool {
	return s.game == "" || meta.Game == s.game
}

// stamp forces the owning game onto a meta about to be written, so a caller
// cannot register a row under another game's namespace.
func (s *store) stamp(meta *Meta) {
	if s.game != "" {
		meta.Game = s.game
	}
}

// Put registers a live entry without writing it.
func (s *store) Put(meta Meta, payload []byte) error {
	if err := validID(meta.ID); err != nil {
		return err
	}
	s.stamp(&meta)
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
	if !ok || !s.owns(e.meta) {
		return Meta{}, nil, false
	}
	return e.meta, e.payload, true
}

// Count is the number of loaded entries.
func (s *store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.mem {
		if s.owns(e.meta) {
			n++
		}
	}
	return n
}

// Delete removes the entry everywhere.
func (s *store) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, inMem := s.mem[id]
	if inMem && !s.owns(e.meta) {
		inMem = false
	}
	if inMem {
		delete(s.mem, id)
	}
	durable, err := s.pgDelete(id)
	if err != nil {
		return err
	}
	if !inMem && !durable {
		return ErrNotFound
	}
	return nil
}

// List merges durable saves with loaded-in-memory entries, newest first.
func (s *store) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := make(map[string]Meta)
	if err := s.pgList(byID); err != nil {
		return nil, err
	}
	for id, e := range s.mem {
		if !s.owns(e.meta) {
			continue
		}
		m := e.meta
		m.Loaded = true
		byID[id] = m
	}
	return sortedMetas(byID), nil
}

// Persist writes the payload, then marks the entry loaded.
func (s *store) Persist(meta Meta, payload []byte) error {
	if err := validID(meta.ID); err != nil {
		return err
	}
	if len(payload) == 0 || !json.Valid(payload) {
		return fmt.Errorf("%w: payload for %s is not JSON", ErrInvalid, meta.ID)
	}
	s.stamp(&meta)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mem[meta.ID]; !ok {
		if err := s.roomLocked(); err != nil {
			return err
		}
	}
	if err := s.pgPersist(meta, payload); err != nil {
		return err
	}
	meta.Loaded = true
	s.mem[meta.ID] = entry{meta: meta, payload: payload}
	return nil
}

// Load returns an entry, reading PostgreSQL when it is not in memory.
func (s *store) Load(id string) (Meta, []byte, error) {
	if err := validID(id); err != nil {
		return Meta{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Always read durable storage: another process (or the same one via a
	// separate adapter) may have written a newer payload.
	meta, payload, err := s.pgLoad(id)
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
	oldest := oldestLocked(s.mem)
	if oldest == "" {
		return ErrLimit
	}
	delete(s.mem, oldest)
	_, _ = s.pgDelete(oldest)
	return nil
}

func (s *store) pgPersist(meta Meta, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("sessionstore: encode meta %s: %w", meta.ID, err)
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO platform_sessions (id, meta, payload, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (id) DO UPDATE SET meta = EXCLUDED.meta, payload = EXCLUDED.payload, updated_at = now()`,
		meta.ID, string(metaJSON), payload)
	if err != nil {
		return fmt.Errorf("sessionstore: persist %s: %w", meta.ID, err)
	}
	return nil
}

func (s *store) pgLoad(id string) (Meta, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	var metaJSON, payload []byte
	err := s.pool.QueryRow(ctx,
		`SELECT meta, payload FROM platform_sessions WHERE id = $1`, id).Scan(&metaJSON, &payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Meta{}, nil, ErrNotFound
		}
		return Meta{}, nil, fmt.Errorf("sessionstore: load %s: %w", id, err)
	}
	var meta Meta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return Meta{}, nil, fmt.Errorf("sessionstore: decode meta %s: %w", id, err)
	}
	meta.ID = id
	if !s.owns(meta) {
		// Another game's row: hide it exactly like a missing save so a scoped
		// store cannot read or overwrite it.
		return Meta{}, nil, ErrNotFound
	}
	return meta, payload, nil
}

func (s *store) pgDelete(id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM platform_sessions WHERE id = $1 AND ($2 = '' OR meta->>'game' = $2)`,
		id, s.game)
	if err != nil {
		return false, fmt.Errorf("sessionstore: delete %s: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *store) pgList(dst map[string]Meta) error {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx,
		`SELECT id, meta FROM platform_sessions WHERE ($1 = '' OR meta->>'game' = $1)`, s.game)
	if err != nil {
		return fmt.Errorf("sessionstore: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var metaJSON []byte
		if err := rows.Scan(&id, &metaJSON); err != nil {
			return fmt.Errorf("sessionstore: scan: %w", err)
		}
		var meta Meta
		if err := json.Unmarshal(metaJSON, &meta); err != nil {
			return fmt.Errorf("sessionstore: decode meta %s: %w", id, err)
		}
		meta.ID = id
		meta.Loaded = false
		dst[id] = meta
	}
	return rows.Err()
}

// sortedMetas orders metas newest-created first, id ascending on ties.
func sortedMetas(byID map[string]Meta) []Meta {
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
	return out
}
