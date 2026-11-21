package session

import (
	"context"
	"testing"

	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func TestLoadDropsImplausibleAccountFields(t *testing.T) {
	pool := pgtest.Pool(t)
	fp := NewPostgresPersistence(pool)
	if err := fp.Save(fixture(t, idA)); err != nil {
		t.Fatal(err)
	}
	// A hand-edited row must not smuggle odd strings into later lookups. The
	// account row only exists so the foreign key accepts the implausible id;
	// restoreSession is what rejects it.
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO accounts (id, provider, provider_user_id, handle, display_name, created_at)
		 VALUES ('../../etc', 'password', '../../etc', 'implausible', 'implausible', now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET account_id = '../../etc', owner_hash = 'not-a-hash' WHERE id = $1`, idA); err != nil {
		t.Fatal(err)
	}

	got, err := fp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "" || got.OwnerHash != "" {
		t.Fatalf("kept account %q hash %q from a hand-edited row", got.AccountID, got.OwnerHash)
	}
}
