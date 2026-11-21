package service

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wricardo/drbrain/games/music/game/engine"
	"github.com/wricardo/drbrain/games/music/game/score"
	"github.com/wricardo/drbrain/platform/mapstore"
)

const (
	// DefaultMaxDisplayName is the display-name limit in characters.
	DefaultMaxDisplayName = 40

	// DefaultListLimit and MaxListLimit bound ListSessions.
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// Options tunes New. The zero value selects the defaults.
type Options struct {
	// SessionTTL: on New, sessions whose LastActionAt is older than this are
	// deleted. 0 (default) disables sweeping.
	SessionTTL time.Duration
	// MaxDisplayName is the maximum display-name length in characters
	// (default DefaultMaxDisplayName).
	MaxDisplayName int
}

// gameService implements GameService.
//
// Concurrency model: every Session is guarded by its own lock. The mutate
// pattern is lock -> mutate -> recompute -> touch (Seq++) -> Clone -> unlock ->
// persist -> broadcast: nothing persists or broadcasts while holding a lock.
type gameService struct {
	pieces   PieceStore
	sessions SessionStore
	bc       Broadcaster // may be nil
	opts     Options
}

// New creates the game service. bc may be nil (no subscribers). If
// opts.SessionTTL > 0 expired sessions already present in sessions (e.g. just
// loaded by session.Manager.LoadAll) are swept before New returns.
func New(pieces PieceStore, sessions SessionStore, bc Broadcaster, opts Options) GameService {
	if opts.MaxDisplayName <= 0 {
		opts.MaxDisplayName = DefaultMaxDisplayName
	}
	svc := &gameService{pieces: pieces, sessions: sessions, bc: bc, opts: opts}
	if opts.SessionTTL > 0 {
		svc.sweepExpired(time.Now().Add(-opts.SessionTTL))
	}
	return svc
}

// sweepExpired deletes sessions idle since before cutoff.
func (svc *gameService) sweepExpired(cutoff time.Time) {
	for _, s := range svc.sessions.List() {
		s.Lock()
		expired := s.LastActionAt.Before(cutoff)
		id := s.ID
		s.Unlock()
		if !expired {
			continue
		}
		if err := svc.DeleteSession(id); err != nil {
			log.Printf("service: sweep expired session %s: %v", id, err)
		}
	}
}

// ---- helpers ---------------------------------------------------------------

func notFound(id string) *Error { return Errorf(CodeNotFound, "session %q not found", id) }

func (svc *gameService) get(id string) (*Session, error) {
	s, ok := svc.sessions.Get(id)
	if !ok {
		return nil, notFound(id)
	}
	return s, nil
}

// persist writes s durably; failures are logged, the in-memory state stays
// authoritative. s must not be locked by the caller.
func (svc *gameService) persist(s *Session) {
	if err := svc.sessions.Persist(s); err != nil {
		log.Printf("service: persist session %s: %v", s.ID, err)
	}
}

func (svc *gameService) broadcast(snap *Session) {
	if svc.bc != nil {
		svc.bc.Broadcast(snap)
	}
}

// touch records a state change: Seq++ and LastActionAt. Lock must be held.
func touch(s *Session) {
	s.Seq++
	s.LastActionAt = time.Now()
}

// solved reports whether the current arrangement sounds like the piece.
func solved(s *Session) bool {
	return (&engine.Puzzle{Piece: s.Piece, Slots: s.Slots}).Solved()
}

func (svc *gameService) cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n > svc.opts.MaxDisplayName {
		return "", Errorf(CodeInvalidArgument, "display name too long (%d characters, max %d)", n, svc.opts.MaxDisplayName)
	}
	return name, nil
}

func newSessionID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newSeed draws a positive 63-bit seed.
func newSeed() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("generate seed: %w", err)
	}
	seed := int64(binary.LittleEndian.Uint64(b[:]) & (1<<63 - 1))
	if seed == 0 {
		seed = 1
	}
	return seed, nil
}

func (svc *gameService) excerpt(id string) (*score.Piece, error) {
	p, err := svc.pieces.Get(id)
	if err != nil {
		if errors.Is(err, mapstore.ErrNotFound) {
			return nil, Errorf(CodeNotFound, "excerpt %q not found", id)
		}
		return nil, Errorf(CodeInvalidArgument, "excerpt %q: %v", id, err)
	}
	return p, nil
}

// ---- sessions --------------------------------------------------------------

// CreateSession starts a challenge on a private snapshot of pieceID.
func (svc *gameService) CreateSession(pieceID string, d engine.Difficulty, displayName string, seed int64) (*Session, error) {
	name, err := svc.cleanName(displayName)
	if err != nil {
		return nil, err
	}
	if d < engine.Novice || d > engine.Genius {
		return nil, Errorf(CodeInvalidArgument, "invalid difficulty %d", int(d))
	}
	piece, err := svc.excerpt(pieceID)
	if err != nil {
		return nil, err
	}
	if seed <= 0 {
		seed, err = newSeed()
		if err != nil {
			return nil, err
		}
	}
	puzzle, err := engine.Generate(piece, d, seed)
	if err != nil {
		return nil, Errorf(CodeInvalidArgument, "cannot build a %s puzzle for %q: %v", d, pieceID, err)
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s := &Session{
		ID: id, DisplayName: name, Difficulty: d, Seed: seed, Piece: piece,
		Slots: append([]engine.Placement(nil), puzzle.Slots...),
		Seq:   1, CreatedAt: now, LastActionAt: now,
	}
	if err := svc.sessions.Put(s); err != nil {
		return nil, err
	}
	svc.persist(s)
	s.Lock()
	snap := s.Clone()
	s.Unlock()
	svc.broadcast(snap)
	return snap, nil
}

// GetSession returns a snapshot of the session.
func (svc *gameService) GetSession(id string) (*Session, error) {
	s, err := svc.get(id)
	if err != nil {
		return nil, err
	}
	s.Lock()
	defer s.Unlock()
	return s.Clone(), nil
}

// ListSessions returns snapshots of the sessions matching f.
func (svc *gameService) ListSessions(f SessionFilter) ([]*Session, error) {
	limit := f.Limit
	switch {
	case limit < 0:
		return nil, Errorf(CodeInvalidArgument, "limit must be >= 0")
	case limit == 0:
		limit = DefaultListLimit
	case limit > MaxListLimit:
		limit = MaxListLimit
	}
	byCreated := false
	switch strings.ToLower(f.Sort) {
	case "", "recent":
	case "created":
		byCreated = true
	default:
		return nil, Errorf(CodeInvalidArgument, "unknown sort %q (want recent or created)", f.Sort)
	}

	type entry struct {
		s   *Session
		id  string
		key time.Time
	}
	var entries []entry
	for _, s := range svc.sessions.List() {
		s.Lock()
		if f.PieceID == "" || s.Piece.ID == f.PieceID {
			key := s.LastActionAt
			if byCreated {
				key = s.CreatedAt
			}
			entries = append(entries, entry{s: s, id: s.ID, key: key})
		}
		s.Unlock()
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].key.Equal(entries[j].key) {
			return entries[i].key.After(entries[j].key)
		}
		return entries[i].id < entries[j].id
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]*Session, len(entries))
	for i, e := range entries {
		e.s.Lock()
		out[i] = e.s.Clone()
		e.s.Unlock()
	}
	return out, nil
}

// DeleteSession removes the session from memory and storage.
func (svc *gameService) DeleteSession(id string) error {
	return svc.sessions.Delete(id)
}

// RenameSession sets the display name.
func (svc *gameService) RenameSession(id, name string) (*Session, error) {
	name, err := svc.cleanName(name)
	if err != nil {
		return nil, err
	}
	s, err := svc.get(id)
	if err != nil {
		return nil, err
	}
	s.Lock()
	s.DisplayName = name
	touch(s)
	snap := s.Clone()
	s.Unlock()
	svc.persist(s)
	svc.broadcast(snap)
	return snap, nil
}

// ---- arrangement -----------------------------------------------------------

// mutate applies one arrangement change under the session lock, recomputes the
// solved state and fans the clone out. A false->true solved transition counts
// a win exactly once; a mutation that changes nothing still bumps Seq.
func (svc *gameService) mutate(id, kind string, a, b int, apply func(*Session) error) (*Session, error) {
	s, err := svc.get(id)
	if err != nil {
		return nil, err
	}
	s.Lock()
	wasSolved := s.Solved
	if err := apply(s); err != nil {
		s.Unlock()
		return nil, err
	}
	change := &Change{Kind: kind, A: a, B: b}
	s.Solved = solved(s)
	if !wasSolved && s.Solved {
		s.Wins++
		change.Kind = KindSolve
	}
	s.LastChange = change
	touch(s)
	snap := s.Clone()
	s.Unlock()
	svc.persist(s)
	svc.broadcast(snap)
	return snap, nil
}

// SwitchMeasures swaps two slots of the arrangement.
func (svc *gameService) SwitchMeasures(id string, a, b int) (*Session, error) {
	return svc.mutate(id, KindSwitch, a, b, func(s *Session) error {
		if a < 0 || a >= len(s.Slots) || b < 0 || b >= len(s.Slots) {
			return Errorf(CodeInvalidArgument, "switch %d/%d outside 0..%d", a, b, len(s.Slots)-1)
		}
		s.Slots[a], s.Slots[b] = s.Slots[b], s.Slots[a]
		return nil
	})
}

// TransformMeasure replaces one slot's flips.
func (svc *gameService) TransformMeasure(id string, index int, t engine.Transform) (*Session, error) {
	return svc.mutate(id, KindTransform, index, 0, func(s *Session) error {
		if index < 0 || index >= len(s.Slots) {
			return Errorf(CodeInvalidArgument, "slot %d outside 0..%d", index, len(s.Slots)-1)
		}
		s.Slots[index].Transform = t
		return nil
	})
}

// SetArrangement replaces the whole arrangement atomically.
func (svc *gameService) SetArrangement(id string, slots []engine.Placement) (*Session, error) {
	return svc.mutate(id, KindArrange, 0, 0, func(s *Session) error {
		if err := validateArrangement(s.Slots, slots); err != nil {
			return err
		}
		s.Slots = append([]engine.Placement(nil), slots...)
		return nil
	})
}

// validateArrangement requires slots to be a permutation of the current
// measure ids: same length, every id exactly once, no foreign ids.
func validateArrangement(current, slots []engine.Placement) error {
	if len(slots) != len(current) {
		return Errorf(CodeInvalidArgument, "arrangement has %d slots, want %d", len(slots), len(current))
	}
	pending := make(map[string]int, len(current))
	for _, p := range current {
		pending[p.MeasureID]++
	}
	for _, p := range slots {
		pending[p.MeasureID]--
		if pending[p.MeasureID] < 0 {
			return Errorf(CodeInvalidArgument, "measure %q is not part of this challenge", p.MeasureID)
		}
	}
	for id, n := range pending {
		if n != 0 {
			return Errorf(CodeInvalidArgument, "measure %q is missing from the arrangement", id)
		}
	}
	return nil
}

// ResetArrangement restores the scramble the session was generated with. The
// challenge is derived, so regenerating with the stored seed is exact.
func (svc *gameService) ResetArrangement(id string) (*Session, error) {
	return svc.mutate(id, KindReset, 0, 0, func(s *Session) error {
		puzzle, err := engine.Generate(s.Piece, s.Difficulty, s.Seed)
		if err != nil {
			return Errorf(CodeInvalidArgument, "reset %q: %v", id, err)
		}
		s.Slots = append([]engine.Placement(nil), puzzle.Slots...)
		return nil
	})
}

// ---- excerpts --------------------------------------------------------------

// ListExcerpts returns every catalogue excerpt, sorted by id.
func (svc *gameService) ListExcerpts() []*score.Piece {
	entries, err := svc.pieces.List()
	if err != nil {
		log.Printf("service: list excerpts: %v", err)
		return nil
	}
	out := make([]*score.Piece, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Piece)
	}
	return out
}

// GetExcerpt returns one catalogue excerpt.
func (svc *gameService) GetExcerpt(id string) (*score.Piece, error) {
	return svc.excerpt(id)
}

// ---- shutdown --------------------------------------------------------------

// Shutdown persists every session. It is idempotent.
func (svc *gameService) Shutdown() {
	for _, s := range svc.sessions.List() {
		svc.persist(s)
	}
}
