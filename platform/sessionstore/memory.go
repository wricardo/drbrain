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
	max   int
	evict bool
	mem   map[string]entry
}

var _ Store = (*memoryStore)(nil)

// NewMemory returns an in-memory Store with the same limits as Open. Tests use
// it when they do not exercise durability.
func NewMemory(opts Options) Store {
	return &memoryStore{max: opts.MaxSessions, evict: opts.Evict, mem: make(map[string]entry)}
}

func (s *memoryStore) Put(meta Meta, payload []byte) error {
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

func (s *memoryStore) Get(id string) (Meta, []byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.mem[id]
	return e.meta, e.payload, ok
}

func (s *memoryStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mem)
}

func (s *memoryStore) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mem[id]; !ok {
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
	if !ok {
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
