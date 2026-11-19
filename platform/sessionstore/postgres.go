package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

const queryTimeout = 10 * time.Second

// initPostgres creates the shared table. It stores no game types: meta is the
// platform's Meta and payload is the game's own JSON.
func (s *store) initPostgres() error {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS platform_sessions (
    id         text PRIMARY KEY,
    meta       jsonb NOT NULL,
    payload    bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
)`)
	if err != nil {
		return fmt.Errorf("sessionstore: create table: %w", err)
	}
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
	return meta, payload, nil
}

func (s *store) pgDelete(id string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	tag, err := s.pool.Exec(ctx, `DELETE FROM platform_sessions WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("sessionstore: delete %s: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *store) pgList(dst map[string]Meta) error {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id, meta FROM platform_sessions`)
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

// pgEvictOldest deletes the least recently active stored session. The in-memory
// map is pruned by the caller.
func (s *store) pgEvictOldest() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM platform_sessions ORDER BY (meta->>'last_action_at')::timestamptz ASC LIMIT 1`)
	if err != nil {
		return "", fmt.Errorf("sessionstore: evict lookup: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", ErrNotFound
	}
	var id string
	if err := rows.Scan(&id); err != nil {
		return "", err
	}
	rows.Close()
	if _, err := s.pool.Exec(ctx, `DELETE FROM platform_sessions WHERE id = $1`, id); err != nil {
		return "", fmt.Errorf("sessionstore: evict %s: %w", id, err)
	}
	return id, nil
}

// sortedIDs is a small helper for deterministic eviction fallbacks.
func sortedIDs(m map[string]entry) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
