// Package jsonmigrate moves the game's runtime state between the JSON-file
// layout (sessions/, accounts/, maps-user/) and PostgreSQL.
//
// Import is the one-shot cutover and is safe to re-run: it only inserts what the
// database does not have yet. Export writes the database back out as files, the
// rollback path before (and after) a cutover. Neither touches the shipped maps.
package jsonmigrate

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/motor/game/config"
	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/session"
	"github.com/wricardo/drbrain/platform/account"
)

// Dirs names the JSON-file layout, the same directories the server flags use.
type Dirs struct {
	Maps     string // shipped maps (read only; tells shipped ids apart from player maps)
	UserMaps string // player maps
	Sessions string
	Accounts string // holds accounts/ and logins/
}

// Counts reports what a run did, per kind.
type Counts struct {
	Maps, Accounts, Logins, Sessions int
	// Skipped counts records left alone: already in the target, or unreadable
	// (those are also logged).
	Skipped int
	// Guests counts sessions imported without their account link because that
	// account is not in the database.
	Guests int
}

func (c Counts) String() string {
	return fmt.Sprintf("maps=%d accounts=%d logins=%d sessions=%d (guest-linked %d) skipped=%d",
		c.Maps, c.Accounts, c.Logins, c.Sessions, c.Guests, c.Skipped)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Import copies player maps, accounts, logins and sessions from the files into
// the database, in that order (sessions reference accounts). It skips anything
// the database already has, so it can run again and again. With dryRun it only
// counts what it would insert.
func Import(ctx context.Context, pool *pgxpool.Pool, d Dirs, dryRun bool) (Counts, error) {
	var c Counts

	// Player maps. config.NewManager creates missing directories, so only open
	// the ones that exist.
	if dirExists(d.UserMaps) && dirExists(d.Maps) {
		m, err := config.NewManager(config.Options{Dir: d.Maps, UserDir: d.UserMaps})
		if err != nil {
			return c, fmt.Errorf("read maps: %w", err)
		}
		store := config.NewPostgresUserMaps(pool)
		for _, mp := range m.List() {
			if m.Builtin(mp.ID) {
				continue
			}
			if dryRun {
				c.Maps++
				continue
			}
			added, err := store.Insert(mp.Config())
			if err != nil {
				return c, err
			}
			if added {
				c.Maps++
			} else {
				c.Skipped++
			}
		}
	}

	// Accounts and logins.
	accountDir := filepath.Join(d.Accounts, "accounts")
	if dirExists(accountDir) {
		files, err := account.Open(d.Accounts, nil)
		if err != nil {
			return c, fmt.Errorf("read accounts: %w", err)
		}
		pg := account.NewPostgresStore(pool)
		inFiles := make(map[string]bool)
		for _, a := range files.Accounts() {
			inFiles[a.ID] = true
			if _, ok := pg.Get(a.ID); ok {
				c.Skipped++
				continue
			}
			if dryRun {
				c.Accounts++
				continue
			}
			switch err := pg.Create(a); err {
			case nil:
				c.Accounts++
			case account.ErrHandleTaken, account.ErrIdentityExists:
				log.Printf("import: skipping account %s: %v", a.ID, err)
				c.Skipped++
			default:
				return c, fmt.Errorf("import account %s: %w", a.ID, err)
			}
		}
		for _, l := range files.Logins() {
			if _, ok := pg.GetLogin(l.TokenHash); ok {
				c.Skipped++
				continue
			}
			if _, ok := pg.Get(l.AccountID); !ok && !(dryRun && inFiles[l.AccountID]) {
				c.Skipped++ // its account was not imported
				continue
			}
			if dryRun {
				c.Logins++
				continue
			}
			if err := pg.PutLogin(l); err != nil {
				return c, fmt.Errorf("import login: %w", err)
			}
			c.Logins++
		}
	}

	// Sessions.
	if dirExists(d.Sessions) {
		fp, err := session.NewFilePersistence(d.Sessions)
		if err != nil {
			return c, err
		}
		pgs := session.NewPostgresPersistence(pool)
		have, err := pgs.ListAll()
		if err != nil {
			return c, err
		}
		stored := make(map[string]bool, len(have))
		for _, id := range have {
			stored[id] = true
		}
		ids, err := fp.ListAll()
		if err != nil {
			return c, err
		}
		pga := account.NewPostgresStore(pool)
		for _, id := range ids {
			if stored[id] {
				c.Skipped++
				continue
			}
			s, err := fp.Load(id)
			if err != nil {
				log.Printf("import: skipping session %s: %v", id, err)
				c.Skipped++
				continue
			}
			if s.AccountID != "" {
				if _, ok := pga.Get(s.AccountID); !ok && !dryRun {
					c.Guests++ // Save stores the row without the dead link
				}
			}
			if dryRun {
				c.Sessions++
				continue
			}
			if err := pgs.Save(s); err != nil {
				return c, fmt.Errorf("import session %s: %w", id, err)
			}
			c.Sessions++
		}
	}
	return c, nil
}

// Export writes the database as files into d: player maps, accounts and
// logins, sessions. Each target directory must hold no .json files yet, so an
// export can never overwrite live data; point it at fresh directories.
func Export(ctx context.Context, pool *pgxpool.Pool, d Dirs) (Counts, error) {
	var c Counts
	for _, dir := range []string{d.UserMaps, filepath.Join(d.Accounts, "accounts"), filepath.Join(d.Accounts, "logins"), d.Sessions} {
		if hasJSON(dir) {
			return c, fmt.Errorf("export: %s already holds .json files; export into empty directories", dir)
		}
	}

	// Player maps.
	if err := os.MkdirAll(d.UserMaps, 0o755); err != nil {
		return c, err
	}
	store := config.NewPostgresUserMaps(pool)
	cfgs, err := store.List()
	if err != nil {
		return c, err
	}
	for _, cfg := range cfgs {
		m, err := engine.NewMap(cfg)
		if err != nil || !engine.ValidID(cfg.ID) {
			log.Printf("export: skipping map %q: %v", cfg.ID, err)
			c.Skipped++
			continue
		}
		data, err := json.MarshalIndent(m.Config(), "", "  ")
		if err != nil {
			return c, err
		}
		if err := os.WriteFile(filepath.Join(d.UserMaps, cfg.ID+".json"), append(data, '\n'), 0o644); err != nil {
			return c, err
		}
		c.Maps++
	}

	// Accounts and logins, through the file store so the layout is its own.
	pga := account.NewPostgresStore(pool)
	files, err := account.Open(d.Accounts, nil)
	if err != nil {
		return c, err
	}
	accounts, err := pga.Accounts()
	if err != nil {
		return c, err
	}
	for _, a := range accounts {
		if err := files.Create(a); err != nil {
			return c, fmt.Errorf("export account %s: %w", a.ID, err)
		}
		c.Accounts++
	}
	logins, err := pga.Logins(time.Now())
	if err != nil {
		return c, err
	}
	for _, l := range logins {
		if err := files.PutLogin(l); err != nil {
			return c, fmt.Errorf("export login: %w", err)
		}
		c.Logins++
	}

	// Sessions.
	pgs := session.NewPostgresPersistence(pool)
	fp, err := session.NewFilePersistence(d.Sessions)
	if err != nil {
		return c, err
	}
	ids, err := pgs.ListAll()
	if err != nil {
		return c, err
	}
	for _, id := range ids {
		s, err := pgs.Load(id)
		if err != nil {
			log.Printf("export: skipping session %s: %v", id, err)
			c.Skipped++
			continue
		}
		if err := fp.Save(s); err != nil {
			return c, fmt.Errorf("export session %s: %w", id, err)
		}
		c.Sessions++
	}
	return c, nil
}

func hasJSON(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			return true
		}
	}
	return false
}
