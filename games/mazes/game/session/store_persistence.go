package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// SchemaVersion is the current payload format version of a stored session.
const SchemaVersion = 1

// PersistedSession is the JSON payload of a session.
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
	// Ownership. owner_token_hash is the SHA-256 of the secret owner token; the
	// token itself is never stored or returned by a read. Both are omitted for
	// pre-ownership files, which stay open to anyone.
	OwnerAccountID string `json:"owner_account_id,omitempty"`
	OwnerTokenHash string `json:"owner_token_hash,omitempty"`
}

// StorePersistence adapts platform/sessionstore to this game: the store owns
// PostgreSQL rows, limits and listing; mazes keeps its JSON shape and
// validation.
type StorePersistence struct {
	store sessionstore.Store
}

var _ SessionPersistence = (*StorePersistence)(nil)

// NewPostgresPersistence opens the PostgreSQL-backed store.
func NewPostgresPersistence(pool *pgxpool.Pool, maxSessions int) (*StorePersistence, error) {
	store, err := sessionstore.Open(sessionstore.Options{Pool: pool, MaxSessions: maxSessions})
	if err != nil {
		return nil, err
	}
	return &StorePersistence{store: store}, nil
}

// Save writes the snapshot to PostgreSQL. s must not be mutated concurrently;
// the Manager passes a private clone.
func (sp *StorePersistence) Save(s *service.Session) (err error) {
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
		SchemaVersion:  SchemaVersion,
		ID:             s.ID,
		DisplayName:    s.DisplayName,
		Map:            s.Map.Config(),
		State:          s.State,
		Seq:            s.Seq,
		CreatedAt:      s.CreatedAt,
		LastActionAt:   s.LastActionAt,
		Attempts:       s.Attempts,
		BestMoves:      s.BestMoves,
		OwnerAccountID: s.OwnerAccountID,
		OwnerTokenHash: s.OwnerTokenHash,
	})
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	return sp.store.Persist(sessionstore.Meta{
		ID: s.ID, DisplayName: s.DisplayName, MapID: s.Map.ID,
		CreatedAt: s.CreatedAt, LastActionAt: s.LastActionAt, Seq: s.Seq,
	}, data)
}

// Load reads and fully validates one stored session.
func (sp *StorePersistence) Load(id string) (*service.Session, error) {
	if !ValidID(id) {
		return nil, ErrInvalidID
	}
	_, raw, err := sp.store.Load(id)
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
		return nil, fmt.Errorf("session %s contains id %q", id, ps.ID)
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
		OwnerAccountID: ps.OwnerAccountID, OwnerTokenHash: ps.OwnerTokenHash,
	}, nil
}

// Delete removes a stored session. It returns ErrNotFound if there is none.
func (sp *StorePersistence) Delete(id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	if err := sp.store.Delete(id); err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("remove session %s: %w", id, err)
	}
	return nil
}

// ListAll returns the ids of every stored session with a valid id.
func (sp *StorePersistence) ListAll() ([]string, error) {
	metas, err := sp.store.List()
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
// a hand-edited or corrupted payload is skipped instead of breaking the
// engine later. It replays st.Path: every step must be an adjacent walkable
// tile followed by its stairs/portal landing, and the counters and status must
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
