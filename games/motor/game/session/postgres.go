package session

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/internal/pgdb"
)

// PostgresPersistence stores sessions in the sessions table. It replaces
// FilePersistence behind the same interface: the Manager still keeps the live
// sessions in memory and writes a snapshot through after each change.
//
// Composite values (map snapshot, program, VM state, recorded wins) are jsonb
// holding the same JSON the file format uses, so a row is validated by the same
// code as a file (restoreSession).
type PostgresPersistence struct {
	pool *pgxpool.Pool
}

var _ SessionPersistence = (*PostgresPersistence)(nil)

// NewPostgresPersistence uses an already opened and migrated pool (pgdb.Open).
func NewPostgresPersistence(pool *pgxpool.Pool) *PostgresPersistence {
	return &PostgresPersistence{pool: pool}
}

// Save inserts or replaces the snapshot. An account link to an account that no
// longer exists is stored as a guest row (NULL), which is what a deleted
// account leaves behind anyway.
func (p *PostgresPersistence) Save(s *service.Session) error {
	ps, err := persistedFrom(s)
	if err != nil {
		return err
	}
	mapJSON, err := json.Marshal(ps.Map)
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	progJSON, err := json.Marshal(ps.Program)
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	vmJSON, err := json.Marshal(ps.VM)
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	slots, err := optionalJSON(ps.FewestSlots)
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}
	steps, err := optionalJSON(ps.FewestSteps)
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", s.ID, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	_, err = p.pool.Exec(ctx, `
INSERT INTO sessions (id, display_name, map_id, map_fingerprint, map, program, vm, speed_ms, seq,
                      created_at, last_action_at, attempts, best_steps, fewest_slots, fewest_steps,
                      account_id, owner_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
        (SELECT id FROM accounts WHERE id = $16), $17)
ON CONFLICT (id) DO UPDATE SET
    display_name = EXCLUDED.display_name, map_id = EXCLUDED.map_id, map_fingerprint = EXCLUDED.map_fingerprint,
    map = EXCLUDED.map, program = EXCLUDED.program, vm = EXCLUDED.vm, speed_ms = EXCLUDED.speed_ms,
    seq = EXCLUDED.seq, last_action_at = EXCLUDED.last_action_at, attempts = EXCLUDED.attempts,
    best_steps = EXCLUDED.best_steps, fewest_slots = EXCLUDED.fewest_slots, fewest_steps = EXCLUDED.fewest_steps,
    account_id = EXCLUDED.account_id, owner_hash = EXCLUDED.owner_hash`,
		s.ID, s.DisplayName, s.MapID, s.Map.Fingerprint(), mapJSON, progJSON, vmJSON, s.SpeedMs, int64(s.Seq),
		s.CreatedAt, s.LastActionAt, s.Attempts, s.BestSteps, slots, steps, s.AccountID, s.OwnerHash)
	if err != nil {
		return fmt.Errorf("save session %s: %w", s.ID, err)
	}
	return nil
}

func optionalJSON(w *service.Win) ([]byte, error) {
	if w == nil {
		return nil, nil
	}
	return json.Marshal(w)
}

const sessionColumns = `id, display_name, map, program, vm, speed_ms, seq, created_at, last_action_at,
       attempts, best_steps, fewest_slots, fewest_steps, COALESCE(account_id, ''), owner_hash`

// Load reads and validates one session. It returns ErrNotFound if absent.
func (p *PostgresPersistence) Load(id string) (*service.Session, error) {
	if !ValidID(id) {
		return nil, ErrInvalidID
	}
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	row := p.pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, id)
	ps, err := scanPersisted(row)
	if err != nil {
		if pgdb.IsNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	return restoreSession(id, ps)
}

func scanPersisted(row pgx.Row) (PersistedSession, error) {
	ps := PersistedSession{SchemaVersion: SchemaVersion}
	var mapJSON, progJSON, vmJSON, slots, steps []byte
	var seq int64
	if err := row.Scan(&ps.ID, &ps.DisplayName, &mapJSON, &progJSON, &vmJSON, &ps.SpeedMs, &seq,
		&ps.CreatedAt, &ps.LastActionAt, &ps.Attempts, &ps.BestSteps, &slots, &steps, &ps.AccountID, &ps.OwnerHash); err != nil {
		return PersistedSession{}, err
	}
	ps.Seq = uint64(seq)
	// The driver returns local time; the file format and the API use UTC.
	ps.CreatedAt, ps.LastActionAt = ps.CreatedAt.UTC(), ps.LastActionAt.UTC()
	for _, d := range []struct {
		raw []byte
		to  any
	}{{mapJSON, &ps.Map}, {progJSON, &ps.Program}, {vmJSON, &ps.VM}} {
		if err := json.Unmarshal(d.raw, d.to); err != nil {
			return PersistedSession{}, fmt.Errorf("decode session %s: %w", ps.ID, err)
		}
	}
	if len(slots) > 0 {
		ps.FewestSlots = new(service.Win)
		if err := json.Unmarshal(slots, ps.FewestSlots); err != nil {
			return PersistedSession{}, fmt.Errorf("decode session %s: %w", ps.ID, err)
		}
	}
	if len(steps) > 0 {
		ps.FewestSteps = new(service.Win)
		if err := json.Unmarshal(steps, ps.FewestSteps); err != nil {
			return PersistedSession{}, fmt.Errorf("decode session %s: %w", ps.ID, err)
		}
	}
	return ps, nil
}

// Delete removes one session. It returns ErrNotFound if absent.
func (p *PostgresPersistence) Delete(id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	tag, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("remove session %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAll returns the ids of every stored session, oldest first.
func (p *PostgresPersistence) ListAll() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id FROM sessions ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		if ValidID(id) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}
