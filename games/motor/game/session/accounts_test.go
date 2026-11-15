package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestFileRoundTripKeepsAccountLinkAndOwnerHash(t *testing.T) {
	fp, dir := newFile(t)
	s := fixture(t, idA)
	s.AccountID, s.OwnerHash = "0123456789abcdef01234567", goodHash
	if err := fp.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := fp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != s.AccountID || got.OwnerHash != s.OwnerHash {
		t.Fatalf("got account %q hash %q", got.AccountID, got.OwnerHash)
	}
	// A guest session writes neither key, so old and new files look alike.
	g := fixture(t, idB)
	if err := fp.Save(g); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, idB+".json"))
	if strings.Contains(string(raw), "account_id") || strings.Contains(string(raw), "owner_hash") {
		t.Fatalf("guest file carries account fields: %s", raw)
	}
}

func TestLoadDropsImplausibleAccountFields(t *testing.T) {
	fp, dir := newFile(t)
	s := fixture(t, idA)
	s.AccountID, s.OwnerHash = "../../etc", "not-a-hash"
	if err := fp.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := fp.Load(idA)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountID != "" || got.OwnerHash != "" {
		t.Fatalf("kept account %q hash %q from a hand-edited file", got.AccountID, got.OwnerHash)
	}
	_ = dir
}
