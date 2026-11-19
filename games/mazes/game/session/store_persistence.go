package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// SchemaVersion is the current on-disk format version of a session file.
const SchemaVersion = 1

// PersistedSession is the JSON form of a session (sessions/<id>.json).
//
// LastEvent is transient and deliberately not stored.
type PersistedSession struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	DisplayName   string           `json:"display_name"`
	Map           engine.MapConfig `json:"map"` // snapshot taken when the session was created
	State         engine.State     `json:"state"`
	Seq           uint64           `json:"seq"`
	CreatedAt     time.Time        `json:"created_at"`
	LastActionAt  time.Time        `json:"last_action_at"`
	Attempts      int              `json:"attempts"`
	BestMoves     *int             `json:"best_moves"` // null until the first win
}

// StorePersistence adapts platform/sessionstore to this game: the store owns
// files, limits and listing; mazes keeps its JSON shape and validation.
type StorePersistence struct {
	store sessionstore.Store
}

// FilePersistence is the pre-monorepo name, kept so existing call sites and
// tests keep compiling while the store does the I/O.
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

// NewFilePersistence keeps the old constructor signature; mazes has no limit
// here (the Manager enforces MaxSessions before persisting).
func NewFilePersistence(dir string) (*StorePersistence, error) {
	return NewStorePersistence(dir, 0)
}

// summarize derives the platform Meta from a saved mazes payload.
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
	if !ValidID(s.ID) {
		return ErrInvalidID
	}
	if s.Map == nil {
		return fmt.Errorf("save session %s: missing map snapshot", s.ID)
	}
	data, err := json.Marshal(PersistedSession{
		SchemaVersion: SchemaVersion,
		ID:            s.ID,
		DisplayName:   s.DisplayName,
		Map:           s.Map.Config(),
		State:         s.State,
		Seq:           s.Seq,
		CreatedAt:     s.CreatedAt,
		LastActionAt:  s.LastActionAt,
		Attempts:      s.Attempts,
		BestMoves:     s.BestMoves,
	})
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	return fp.store.Persist(sessionstore.Meta{
		ID: s.ID, DisplayName: s.DisplayName, MapID: s.Map.ID,
		CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt, Seq: s.Seq,
	}, data)
}

// Load reads and fully validates <dir>/<id>.json.
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
	if err := validateState(m, &ps.State); err != nil {
		return nil, fmt.Errorf("session %s: invalid state: %w", id, err)
	}
	if ps.Attempts < 0 {
		return nil, fmt.Errorf("session %s: attempts %d is negative", id, ps.Attempts)
	}
	if ps.BestMoves != nil && (*ps.BestMoves < 1 || *ps.BestMoves > m.MaxMoves) {
		return nil, fmt.Errorf("session %s: best_moves %d out of range 1..%d", id, *ps.BestMoves, m.MaxMoves)
	}
	last := ps.LastActionAt
	if last.IsZero() {
		last = ps.CreatedAt
	}
	return &service.Session{
		ID: ps.ID, DisplayName: ps.DisplayName, MapID: m.ID, Map: m,
		State: ps.State, Seq: ps.Seq, CreatedAt: ps.CreatedAt, LastActionAt: last,
		Attempts: ps.Attempts, BestMoves: ps.BestMoves,
	}, nil
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

// validateState rejects states that are not reachable by playing the map, so
// a hand-edited or corrupted file is skipped instead of breaking the engine
// later. It replays st.Path: every step must be an adjacent walkable tile
// followed by its stairs/portal landing, and the counters and status must
// agree with that route.
func validateState(m *engine.Map, st *engine.State) error {
	switch st.LossReason {
	case engine.LossNone, engine.LossMoveLimit:
	default:
		return fmt.Errorf("unknown loss reason %q", st.LossReason)
	}
	if len(st.Path) == 0 || st.Path[0] != m.Start {
		return fmt.Errorf("path must start at the start tile %v", m.Start)
	}
	if len(st.Path) > 2*m.MaxMoves+1 {
		return fmt.Errorf("path has %d entries, too long for max_moves %d", len(st.Path), m.MaxMoves)
	}
	moves := 0
	cur := m.Start
	for i := 1; i < len(st.Path); i++ {
		q := st.Path[i]
		if !m.InBounds(q) || !m.TileAt(q).Walkable() {
			return fmt.Errorf("path[%d] %v is blocked or out of bounds", i, q)
		}
		if q.Level != cur.Level || abs(q.X-cur.X)+abs(q.Y-cur.Y) != 1 {
			return fmt.Errorf("path[%d] %v is not adjacent to %v", i, q, cur)
		}
		moves++
		cur = q
		var to engine.Position
		switch t := m.TileAt(q); {
		case t == engine.TileUp:
			to = engine.Position{Level: q.Level + 1, X: q.X, Y: q.Y}
		case t == engine.TileDown:
			to = engine.Position{Level: q.Level - 1, X: q.X, Y: q.Y}
		case t.IsPortal():
			to, _ = m.Partner(q)
		default:
			continue
		}
		i++
		if i >= len(st.Path) || st.Path[i] != to {
			return fmt.Errorf("path[%d] must be the landing %v of the tile before it", i, to)
		}
		cur = to
	}
	if st.Pos != cur {
		return fmt.Errorf("position %v does not match the end of the path %v", st.Pos, cur)
	}
	if st.Moves != moves {
		return fmt.Errorf("moves %d does not match the path (%d)", st.Moves, moves)
	}
	if moves > m.MaxMoves {
		return fmt.Errorf("moves %d exceeds max_moves %d", moves, m.MaxMoves)
	}

	want, reason := engine.StatusPlaying, engine.LossNone
	switch {
	case m.TileAt(cur) == engine.TileFinish:
		want = engine.StatusWon
	case moves >= m.MaxMoves:
		want, reason = engine.StatusLost, engine.LossMoveLimit
	case moves == 0:
		want = engine.StatusReady
	}
	if st.Status != want || st.LossReason != reason {
		return fmt.Errorf("status %q/%q does not match the position and moves (want %q/%q)", st.Status, st.LossReason, want, reason)
	}
	return nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
