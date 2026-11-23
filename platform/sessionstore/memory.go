package sessionstore

import (
	"encoding/json"
	"fmt"
	"sync"
)

// memoryStore keeps sessions in memory and nothing on disk. It exists for unit
// tests of game managers: the server always opens the PostgreSQL store, and
// nothing in the binary can select this backend.
type memoryStore struct {
	mu    sync.Mutex
	game  string
	max   int
	evict bool
	mem   map[string]entry
}

var _ Store = (*memoryStore)(nil)

// NewMemory returns an in-memory Store with the same limits as Open. Tests use
// it when they do not exercise durability.
func NewMemory(opts Options) Store {
	return &memoryStore{game: opts.Game, max: opts.MaxSessions, evict: opts.Evict, mem: make(map[string]entry)}
}

// owns reports whether meta belongs to this store's game; an unscoped store
// owns every row.
func (s *memoryStore) owns(meta Meta) bool {
	return s.game == "" || meta.Game == s.game
}

// stamp forces the owning game onto a meta about to be written.
func (s *memoryStore) stamp(meta *Meta) {
	if s.game != "" {
		meta.Game = s.game
	}
}

func (s *memoryStore) Put(meta Meta, payload []byte) error {
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

func (s *memoryStore) Get(id string) (Meta, []byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[id]
	if !ok || !s.owns(e.meta) {
		return Meta{}, nil, false
	}
	return e.meta, e.payload, true
}

func (s *memoryStore) Count() int {
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

func (s *memoryStore) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[id]
	if !ok || !s.owns(e.meta) {
		return ErrNotFound
	}
	delete(s.mem, id)
	return nil
}

func (s *memoryStore) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byID := make(map[string]Meta, len(s.mem))
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

func (s *memoryStore) Persist(meta Meta, payload []byte) error {
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
	meta.Loaded = true
	s.mem[meta.ID] = entry{meta: meta, payload: payload}
	return nil
}

func (s *memoryStore) Load(id string) (Meta, []byte, error) {
	if err := validID(id); err != nil {
		return Meta{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[id]
	if !ok || !s.owns(e.meta) {
		return Meta{}, nil, ErrNotFound
	}
	return e.meta, e.payload, nil
}

func (s *memoryStore) roomLocked() error {
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
	return nil
}
