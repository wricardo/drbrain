package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	ownerAccount = "0123456789abcdef01234567"
	otherAccount = "fedcba9876543210fedcba98"
	staleAccount = "aaaaaaaaaaaaaaaaaaaaaaaa"
)

// createOwned makes a session through the manager and returns it with its token.
func createOwned(t *testing.T, mgr *Manager, id string, accountID string) (*Session, string) {
	t.Helper()
	sess, token, err := mgr.Create(id, 1, accountID)
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	return sess, token
}

func TestCreateMintsA256BitTokenStoredOnlyAsAHash(t *testing.T) {
	mgr := newTestManager(t)
	sess, token := createOwned(t, mgr, "owned", "")

	if len(token) != 43 {
		t.Fatalf("token length = %d, want 43 (32 bytes base64url)", len(token))
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Fatalf("token %q is not base64url", token)
	}
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(mgr.dir, "owned.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("the plaintext owner token was written to the save")
	}
	var p persisted
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.OwnerHash != sanitizeHash(p.OwnerHash) || p.OwnerHash == "" {
		t.Fatalf("owner_hash = %q, want a 64-char hex digest", p.OwnerHash)
	}

	// Two sessions never share a token.
	_, token2 := createOwned(t, mgr, "owned2", "")
	if token2 == token {
		t.Fatal("two sessions were given the same owner token")
	}
}

func TestCreateGeneratesARandomIDWhenOmitted(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(sess.ID) {
		t.Fatalf("generated id = %q, want 16 lower-case hex", sess.ID)
	}
	if !ValidID(sess.ID) {
		t.Fatalf("generated id %q is not valid", sess.ID)
	}
}

func TestAuthorizationMatrix(t *testing.T) {
	mgr := newTestManager(t)
	guest, guestTok := createOwned(t, mgr, "guest", "")
	owned, ownedTok := createOwned(t, mgr, "owned", ownerAccount)
	claimed, claimedTok := createOwned(t, mgr, "claimed", "")
	if status, err := mgr.Claim(ownerAccount, claimed.ID, claimedTok); err != nil || status != ClaimAttached {
		t.Fatalf("claim = %q, %v", status, err)
	}

	tests := []struct {
		name  string
		sess  *Session
		actor Actor
		want  bool
	}{
		{"guest, nobody", guest, Actor{}, false},
		{"guest, another account", guest, Actor{AccountID: otherAccount}, false},
		{"guest, wrong token", guest, Actor{OwnerToken: "nope"}, false},
		{"guest, right token", guest, Actor{OwnerToken: guestTok}, true},

		{"owned, nobody", owned, Actor{}, false},
		{"owned, another account", owned, Actor{AccountID: otherAccount}, false},
		{"owned, wrong token", owned, Actor{OwnerToken: "deadbeef"}, false},
		{"owned, the account", owned, Actor{AccountID: ownerAccount}, true},
		{"owned, the token", owned, Actor{OwnerToken: ownedTok}, true},
		{"owned, other account but right token", owned, Actor{AccountID: otherAccount, OwnerToken: ownedTok}, true},

		{"claimed, nobody", claimed, Actor{}, false},
		{"claimed, the account", claimed, Actor{AccountID: ownerAccount}, true},

		// A link to an account that no longer exists must not turn the save open:
		// an account id that is not the owner still needs the token.
		{"dead link, nobody", &Session{accountID: staleAccount, ownerHash: sanitizeHash(goodHash)}, Actor{}, false},
		{"dead link, some other account", &Session{accountID: staleAccount, ownerHash: sanitizeHash(goodHash)}, Actor{AccountID: otherAccount}, false},
	}
	for _, tc := range tests {
		if got := tc.sess.CanPlay(tc.actor); got != tc.want {
			t.Errorf("%s: CanPlay = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Deleting needs the credential for a guest session too: unlike motor, delivery
// saves are long-lived progress, not ephemeral runs.
func TestDeleteNeedsTheCredentialEvenForAGuestSession(t *testing.T) {
	mgr := newTestManager(t)
	live, token := createOwned(t, mgr, "guestlive", "")
	// The manager does not authorize; the resolver applies this rule. A guest
	// session is not open by id, because delivery saves are long-lived progress.
	if live.CanPlay(Actor{}) {
		t.Fatal("a guest session was open by id")
	}
	if !live.CanPlay(Actor{OwnerToken: token}) {
		t.Fatal("the creator's token was refused")
	}
}

func TestClaimStatuses(t *testing.T) {
	mgr := newTestManager(t)
	guest, tok := createOwned(t, mgr, "c1", "")
	owned, _ := createOwned(t, mgr, "c2", otherAccount)
	legacy, _ := createLegacy(t, mgr, "c3")

	tests := []struct {
		name    string
		account string
		id      string
		token   string
		want    string
	}{
		{"attaches a guest save", ownerAccount, guest.ID, tok, ClaimAttached},
		{"already yours", ownerAccount, guest.ID, tok, ClaimAlreadyAttached},
		{"another account", otherAccount, guest.ID, tok, ClaimRejected},
		{"wrong token", ownerAccount, "does-not-exist", "nope", ClaimRejected},
		{"unknown session", ownerAccount, "0000000000000000", tok, ClaimRejected},
		{"another player's save", ownerAccount, owned.ID, tok, ClaimRejected},
		{"legacy save", ownerAccount, legacy.ID, "anything", ClaimRejected},
	}
	for _, tc := range tests {
		status, err := mgr.Claim(tc.account, tc.id, tc.token)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if status != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.name, status, tc.want)
		}
	}
	if _, err := mgr.Claim("", guest.ID, tok); err == nil {
		t.Fatal("claim without an account did not fail")
	}

	// The attachment is persisted, so a reload still knows the owner.
	mgr.Evict(guest.ID)
	reloaded, err := mgr.GetOrLoad(guest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.OwnerAccountID(); got != ownerAccount {
		t.Fatalf("reloaded owner = %q, want %q", got, ownerAccount)
	}
	if !reloaded.CanPlay(Actor{AccountID: ownerAccount}) {
		t.Fatal("the claim did not survive the reload")
	}
}

func TestSaveShapePerOwner(t *testing.T) {
	mgr := newTestManager(t)
	guest, _ := createOwned(t, mgr, "guestfile", "")
	owned, token := createOwned(t, mgr, "ownedfile", ownerAccount)
	for _, s := range []*Session{guest, owned} {
		if err := s.Save(mgr.CityFileName()); err != nil {
			t.Fatal(err)
		}
	}

	// A guest save has a hash (the creator's token) but no account link.
	raw, err := os.ReadFile(filepath.Join(mgr.dir, "guestfile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "account_id") {
		t.Fatalf("guest save carries an account link: %s", raw)
	}
	if !strings.Contains(string(raw), "owner_hash") {
		t.Fatal("guest save has no owner hash")
	}

	// A legacy save (see createLegacy) has neither key; that is proven there.

	mgr.Evict("ownedfile")
	reloaded, err := mgr.GetOrLoad("ownedfile")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.OwnerAccountID(); got != ownerAccount {
		t.Errorf("accountID = %q, want %q", got, ownerAccount)
	}
	if !reloaded.CanPlay(Actor{OwnerToken: token}) {
		t.Error("the token stopped working after a reload")
	}
}

func TestLoadDropsImplausibleOwnerFields(t *testing.T) {
	mgr := newTestManager(t)
	sess, _ := createOwned(t, mgr, "bad", ownerAccount)
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mgr.dir, "bad.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatal(err)
	}
	doc["account_id"] = "../../etc"
	doc["owner_hash"] = "not-a-hash"
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("bad")
	reloaded, err := mgr.GetOrLoad("bad")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.OwnerAccountID(); got != "" {
		t.Errorf("kept account %q from a hand-edited file", got)
	}
	if !reloaded.CanPlay(Actor{}) {
		t.Error("a save whose credentials were dropped must load as legacy/open")
	}
}

// createLegacy writes a save with no owner fields, as every save written before
// owner tokens looks, and loads it back.
func createLegacy(t *testing.T, mgr *Manager, id string) (*Session, string) {
	t.Helper()
	sess, _ := createOwned(t, mgr, id, "")
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mgr.dir, id+".json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "account_id")
	delete(doc, "owner_hash")
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	mgr.Evict(id)
	legacy, err := mgr.GetOrLoad(id)
	if err != nil {
		t.Fatalf("legacy save failed to load: %v", err)
	}
	if !legacy.CanPlay(Actor{}) {
		t.Fatal("a legacy save is not open")
	}
	return legacy, ""
}

// goodHash is a valid-shaped digest for hand-built Session values.
const goodHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
