package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// SchemaVersion is the current on-disk format version of a session file.
const SchemaVersion = 1

// PersistedSession is the JSON form of a session (sessions/<id>.json).
//
// Playing and LastEvent are transient and deliberately not stored: a restored
// session never resumes by itself.
type PersistedSession struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	DisplayName   string           `json:"display_name"`
	Map           engine.MapConfig `json:"map"` // snapshot taken when the session was created
	Program       engine.Program   `json:"program"`
	VM            engine.VMState   `json:"vm"`
	SpeedMs       int              `json:"speed_ms"`
	Seq           uint64           `json:"seq"`
	CreatedAt     time.Time        `json:"created_at"`
	LastActionAt  time.Time        `json:"last_action_at"`
	Attempts      int              `json:"attempts"`
	BestSteps     int              `json:"best_steps"`
	// FewestSlots and FewestSteps hold the best recorded win under each
	// leaderboard ordering; absent in files written before leaderboards.
	FewestSlots *service.Win `json:"fewest_slots,omitempty"`
	FewestSteps *service.Win `json:"fewest_steps,omitempty"`
	// AccountID links the session to a signed-in player's account; OwnerHash is
	// the SHA-256 of its secret owner token. Absent for guest sessions and for
	// files written before accounts.
	AccountID string `json:"account_id,omitempty"`
	OwnerHash string `json:"owner_hash,omitempty"`
}

// StorePersistence adapts platform/sessionstore to this game. The store owns
// files, limits and listing; motor keeps its JSON shape, validation and
// PostgresPersistence (whose schema predates the platform store).
type StorePersistence struct {
	store sessionstore.Store
}

// FilePersistence is the pre-monorepo name, kept so existing call sites and
// tests keep compiling.
type FilePersistence = StorePersistence

var _ SessionPersistence = (*StorePersistence)(nil)

// NewStorePersistence opens the file-backed store for dir.
func NewStorePersistence(dir string, maxSessions int) (*StorePersistence, error) {
	store, err := sessionstore.Open(sessionstore.Options{Dir: dir, MaxSessions: maxSessions, Summary: summarize})
	if err != nil {
		return nil, err
	}
	return &StorePersistence{store: store}, nil
}

// NewFilePersistence keeps the old constructor signature.
func NewFilePersistence(dir string) (*StorePersistence, error) {
	return NewStorePersistence(dir, 0)
}

// summarize derives the platform Meta from a saved motor payload.
func summarize(raw []byte) (sessionstore.Meta, error) {
	var ps PersistedSession
	if err := json.Unmarshal(raw, &ps); err != nil {
		return sessionstore.Meta{}, err
	}
	last := ps.LastActionAt
	if last.IsZero() {
		last = ps.CreatedAt
	}
	return sessionstore.Meta{
		ID: ps.ID, DisplayName: ps.DisplayName, MapID: ps.Map.ID,
		CreatedAt: ps.CreatedAt, LastActionAt: last, Seq: ps.Seq,
	}, nil
}

// Save atomically writes the snapshot to <dir>/<id>.json (temp file in the
// same directory, fsync, rename). s must not be mutated concurrently; the
// Manager passes a private clone.
func (fp *FilePersistence) Save(s *service.Session) (err error) {
	if s == nil {
		return fmt.Errorf("save session: nil session")
	}
	ps, err := persistedFrom(s)
	if err != nil {
		return err
	}
	data, err := json.Marshal(ps)
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	return fp.store.Persist(sessionstore.Meta{
		ID: s.ID, DisplayName: s.DisplayName, MapID: s.MapID,
		CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt, Seq: s.Seq,
	}, data)
}

// Load reads and fully validates <dir>/<id>.json. The returned session is
// never Playing.
func (fp *FilePersistence) Load(id string) (*service.Session, error) {
	if !ValidID(id) {
		return nil, ErrInvalidID
	}
	_, raw, err := fp.store.Load(id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	var ps PersistedSession
	if err := json.Unmarshal(raw, &ps); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", id, err)
	}
	return restoreSession(id, ps)
}

// persistedFrom is the storable form of a snapshot, shared by every backend.
func persistedFrom(s *service.Session) (PersistedSession, error) {
	if s == nil {
		return PersistedSession{}, fmt.Errorf("save session: nil session")
	}
	if !ValidID(s.ID) {
		return PersistedSession{}, ErrInvalidID
	}
	if s.Map == nil {
		return PersistedSession{}, fmt.Errorf("save session %s: missing map snapshot", s.ID)
	}
	return PersistedSession{
		SchemaVersion: SchemaVersion,
		ID:            s.ID,
		DisplayName:   s.DisplayName,
		Map:           s.Map.Config(),
		Program:       s.Program,
		VM:            s.VM,
		SpeedMs:       s.SpeedMs,
		Seq:           s.Seq,
		CreatedAt:     s.CreatedAt,
		LastActionAt:  s.LastActionAt,
		Attempts:      s.Attempts,
		BestSteps:     s.BestSteps,
		FewestSlots:   s.FewestSlots,
		FewestSteps:   s.FewestSteps,
		AccountID:     s.AccountID,
		OwnerHash:     s.OwnerHash,
	}, nil
}

// restoreSession validates a stored session against its own map snapshot and
// turns it back into a live-shaped session (never Playing). Every backend runs
// its rows through here, so a hand-edited or corrupt record is rejected the
// same way whether it came from a file or the database.
func restoreSession(id string, ps PersistedSession) (*service.Session, error) {
	if ps.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("session %s: unsupported schema_version %d (want %d)", id, ps.SchemaVersion, SchemaVersion)
	}
	if ps.ID != id {
		return nil, fmt.Errorf("session file %s contains id %q", id, ps.ID)
	}
	m, err := engine.NewMap(ps.Map)
	if err != nil {
		return nil, fmt.Errorf("session %s: invalid map snapshot: %w", id, err)
	}
	if err := engine.ValidateProgram(m, ps.Program); err != nil {
		return nil, fmt.Errorf("session %s: %w", id, err)
	}
	if err := validateVM(m, &ps.Program, &ps.VM); err != nil {
		return nil, fmt.Errorf("session %s: invalid vm: %w", id, err)
	}
	if ps.SpeedMs < service.MinSpeedMs || ps.SpeedMs > service.MaxSpeedMs {
		ps.SpeedMs = service.DefaultSpeedMs
	}
	last := ps.LastActionAt
	if last.IsZero() {
		last = ps.CreatedAt
	}
	s := &service.Session{
		ID: ps.ID, DisplayName: ps.DisplayName, MapID: m.ID, Map: m,
		Program: ps.Program, VM: ps.VM, Playing: false, SpeedMs: ps.SpeedMs,
		Seq: ps.Seq, CreatedAt: ps.CreatedAt, LastActionAt: last,
		Attempts: ps.Attempts, BestSteps: ps.BestSteps,
		FewestSlots: sanitizeWin(m, ps.FewestSlots), FewestSteps: sanitizeWin(m, ps.FewestSteps),
		AccountID: sanitizeAccountID(ps.AccountID), OwnerHash: sanitizeHash(ps.OwnerHash),
	}
	// Sessions saved before leaderboards existed: a WON VM identifies its win.
	s.BackfillWin()
	return s, nil
}

// sanitizeWin drops a recorded win that cannot have happened on m (a
// hand-edited or corrupt file must not put an impossible result on a board).
func sanitizeWin(m *engine.Map, w *service.Win) *service.Win {
	if w == nil || w.Steps < 1 || w.Steps > m.MaxSteps || w.Slots < 1 || w.Slots > m.TotalSlots() {
		return nil
	}
	c := *w
	return &c
}

// sanitizeAccountID drops an account link that is not a plausible account id
// (a hand-edited file must not smuggle odd strings into later lookups).
func sanitizeAccountID(id string) string {
	if len(id) == 0 || len(id) > 64 {
		return ""
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return id
}

// sanitizeHash keeps only a 64-character lower-case hex SHA-256.
func sanitizeHash(h string) string {
	if len(h) != 64 {
		return ""
	}
	for _, r := range h {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	return h
}

// Delete removes <dir>/<id>.json. It returns ErrNotFound if there is none.
func (fp *FilePersistence) Delete(id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	if err := fp.store.Delete(id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("remove session %s: %w", id, err)
	}
	return nil
}

// ListAll returns the ids of every <id>.json file with a valid id; other
// files (temp files, strays) are ignored.
func (fp *FilePersistence) ListAll() ([]string, error) {
	metas, err := fp.store.List()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(metas))
	for _, m := range metas {
		if ValidID(m.ID) {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// validateVM rejects states that could make the engine index out of range or
// loop, so a hand-edited or corrupted file is skipped instead of crashing a
// runner later.
func validateVM(m *engine.Map, p *engine.Program, vm *engine.VMState) error {
	switch vm.Status {
	case engine.StatusReady, engine.StatusRunning, engine.StatusWon, engine.StatusLost:
	default:
		return fmt.Errorf("unknown status %q", vm.Status)
	}
	switch vm.LossReason {
	case engine.LossNone, engine.LossStepLimit, engine.LossCallDepth, engine.LossProgramEnded:
	default:
		return fmt.Errorf("unknown loss reason %q", vm.LossReason)
	}
	if vm.Facing < engine.FacingUp || vm.Facing > engine.FacingLeft {
		return fmt.Errorf("facing %d out of range", vm.Facing)
	}
	if m.IsRock(vm.Pos.X, vm.Pos.Y) {
		return fmt.Errorf("position (%d,%d) is blocked or out of bounds", vm.Pos.X, vm.Pos.Y)
	}
	if len(vm.Collected) != len(m.Treats) {
		return fmt.Errorf("collected has %d entries, map has %d treats", len(vm.Collected), len(m.Treats))
	}
	left := 0
	for _, c := range vm.Collected {
		if !c {
			left++
		}
	}
	if vm.TreatsLeft != left {
		return fmt.Errorf("treats_left %d does not match collected (%d uncollected)", vm.TreatsLeft, left)
	}
	if len(vm.DoorsOpen) != len(m.DoorLetters) {
		return fmt.Errorf("doors_open has %d entries, map has %d doors", len(vm.DoorsOpen), len(m.DoorLetters))
	}
	if l := m.DoorAt(vm.Pos.X, vm.Pos.Y); l != 0 {
		i := m.DoorIndex(l)
		if i < 0 || !vm.DoorsOpen[i] {
			return fmt.Errorf("position (%d,%d) is a closed door", vm.Pos.X, vm.Pos.Y)
		}
	}
	if vm.Steps < 0 || vm.Steps > m.MaxSteps {
		return fmt.Errorf("steps %d out of range 0..%d", vm.Steps, m.MaxSteps)
	}
	// One step appends the entry cell and may then append a second position: a
	// portal teleport or a conveyor push (engine.VMState.enter). A turn, a blocked
	// move and a CALL append nothing, so 2*steps+1 is the tight upper bound; a
	// run that used a conveyor or portal is legitimate and must still load.
	if len(vm.Visited) > 2*vm.Steps+1 {
		return fmt.Errorf("visited has %d entries after %d steps", len(vm.Visited), vm.Steps)
	}
	for _, v := range vm.Visited {
		if m.IsRock(v.X, v.Y) {
			return fmt.Errorf("visited (%d,%d) is blocked or out of bounds", v.X, v.Y)
		}
	}
	if len(vm.CallStack) == 0 || len(vm.CallStack) > m.MaxCallDepth+2 {
		return fmt.Errorf("call stack has %d frames", len(vm.CallStack))
	}
	for i, f := range vm.CallStack {
		var tape []engine.Instruction
		switch {
		case i == 0 && f.Tape != -1:
			return fmt.Errorf("call stack[0] must be main, got tape %d", f.Tape)
		case i == 0:
			tape = p.Main
		case f.Tape < 0 || f.Tape >= len(p.Subs):
			return fmt.Errorf("call stack[%d]: tape %d out of range", i, f.Tape)
		default:
			tape = p.Subs[f.Tape]
		}
		if f.PC < 0 || f.PC > len(tape) {
			return fmt.Errorf("call stack[%d]: pc %d out of range 0..%d", i, f.PC, len(tape))
		}
	}
	return nil
}
