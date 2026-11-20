package config

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/platform/pgdb"
)

// PostgresUserMaps is a UserStore on the maps table. It holds player-created
// maps only: shipped maps are never inserted, they are the files in maps/.
type PostgresUserMaps struct {
	pool *pgxpool.Pool
}

var _ UserStore = (*PostgresUserMaps)(nil)

// NewPostgresUserMaps uses an already opened and migrated pool (pgdb.Open).
func NewPostgresUserMaps(pool *pgxpool.Pool) *PostgresUserMaps { return &PostgresUserMaps{pool: pool} }

// List returns every stored player map, oldest first. A row whose JSON does
// not decode is logged by the Manager through the zero config it yields.
func (p *PostgresUserMaps) List() ([]engine.MapConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id, config FROM maps ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list player maps: %w", err)
	}
	defer rows.Close()
	var out []engine.MapConfig
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("list player maps: %w", err)
		}
		var cfg engine.MapConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			// Surface it with the row id so the Manager's validation skips it
			// and logs which row was bad.
			cfg = engine.MapConfig{ID: id}
		}
		if cfg.ID != id {
			// The row key is the truth; a config that disagrees is corrupt.
			cfg = engine.MapConfig{ID: id}
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// Put inserts the map or replaces its config, keeping the original created_at.
func (p *PostgresUserMaps) Put(cfg engine.MapConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal map %s: %w", cfg.ID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	if _, err := p.pool.Exec(ctx, `
INSERT INTO maps (id, config) VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET config = EXCLUDED.config, updated_at = now()`, cfg.ID, raw); err != nil {
		return fmt.Errorf("store map %s: %w", cfg.ID, err)
	}
	return nil
}

// Delete removes the map; an unknown id is not an error.
func (p *PostgresUserMaps) Delete(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	if _, err := p.pool.Exec(ctx, `DELETE FROM maps WHERE id = $1`, id); err != nil {
		return fmt.Errorf("remove map %s: %w", id, err)
	}
	return nil
}

// Insert stores the map only when no map has its id, and reports whether it
// did. The JSON import uses it so a re-run never overwrites a newer map.
func (p *PostgresUserMaps) Insert(cfg engine.MapConfig) (bool, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return false, fmt.Errorf("marshal map %s: %w", cfg.ID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pgdb.QueryTimeout)
	defer cancel()
	tag, err := p.pool.Exec(ctx, `INSERT INTO maps (id, config) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, cfg.ID, raw)
	if err != nil {
		return false, fmt.Errorf("store map %s: %w", cfg.ID, err)
	}
	return tag.RowsAffected() == 1, nil
}
