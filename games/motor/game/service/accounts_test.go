package service_test

import (
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain-motor-programming/game/account"
	"github.com/wricardo/drbrain-motor-programming/game/engine"
	"github.com/wricardo/drbrain-motor-programming/game/service"
)

// clock is a settable clock for login expiry and handle cooldowns.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// acctEnv is an env with accounts switched on (real file-backed store in a temp dir).
type acctEnv struct {
	*env
	store *account.Store
	clk   *clock
}

func newAcctEnv(t *testing.T, opts service.Options) *acctEnv {
	t.Helper()
	clk := &clock{t: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
	store, err := account.Open(t.TempDir(), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	opts.Accounts = store
	opts.Now = clk.Now
	return &acctEnv{env: newEnv(t, envOpts{svcOpts: opts}, wideCfg, corridorCfg), store: store, clk: clk}
}

// signIn creates (or finds) an account for a fake google user.
func (e *acctEnv) signIn(t *testing.T, user string) *service.Account {
	t.Helper()
	a, _, err := e.svc.SignIn("google", user)
	if err != nil {
		t.Fatalf("SignIn(%s): %v", user, err)
	}
	return a
}

// ownedWin creates a session owned by accountID ("" = guest) and wins it with p.
func (e *acctEnv) ownedWin(t *testing.T, accountID, name string, p engine.Program) (id, token string) {
	t.Helper()
	s, tok, err := e.svc.CreateOwnedSession("wide", name, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.winWith(t, s.ID, p); got.VM.Status != engine.StatusWon {
		t.Fatalf("program did not win")
	}
	return s.ID, tok
}

// A signed-in player who starts a session without typing a name gets their
// account's name, not a random guest name: the play and watch pages show the
// session's own displayName, so a guest name there reads as somebody else.
func TestSignedInSessionWithoutANameUsesTheAccountName(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice := e.signIn(t, "alice")

	for name, displayName := range map[string]string{
		"empty":       "",
		"only spaces": "   ",
	} {
		s, _, err := e.svc.CreateOwnedSession("wide", displayName, alice.ID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.DisplayName != alice.DisplayName {
			t.Fatalf("%s: display name = %q, want the account's %q", name, s.DisplayName, alice.DisplayName)
		}
	}

	// A name the player typed still wins over the account's.
	s, _, err := e.svc.CreateOwnedSession("wide", "My Own Name", alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.DisplayName != "My Own Name" {
		t.Fatalf("typed name was replaced: %q", s.DisplayName)
	}

	// Guests keep the random guest name: there is no account to borrow from.
	g, _, err := e.svc.CreateOwnedSession("wide", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if g.DisplayName == "" || g.DisplayName == alice.DisplayName {
		t.Fatalf("guest display name = %q, want a random guest name", g.DisplayName)
	}
}

func TestAccountsOffByDefault(t *testing.T) {
	e := newEnv(t, envOpts{}, wideCfg)
	if e.svc.AccountsEnabled() {
		t.Fatal("AccountsEnabled with no store")
	}
	if _, _, err := e.svc.SignIn("google", "u"); err == nil {
		t.Fatal("SignIn must fail when accounts are off")
	} else {
		wantCode(t, err, service.CodeForbidden)
	}
	if _, ok := e.svc.Authenticate("whatever-token-that-is-long-enough"); ok {
		t.Fatal("Authenticate succeeded with no store")
	}
	if _, err := e.svc.GetProfile("anyone", ""); err == nil {
		t.Fatal("profile found with no store")
	} else {
		wantCode(t, err, service.CodeNotFound)
	}
	// Guest play and the leaderboard are untouched.
	id := e.wonBy(t, "wide", "Zed", wideDirect())
	lb, err := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide"})
	if err != nil || len(lb.Entries) != 1 || lb.Entries[0].SessionID != id || lb.Entries[0].Handle != "" || lb.Entries[0].Mine {
		t.Fatalf("leaderboard = %+v, %v", lb, err)
	}
}

func TestSignInCreatesOnceAndGeneratesValidHandle(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	a, created, err := e.svc.SignIn("google", "sub-1")
	if err != nil || !created {
		t.Fatalf("first SignIn = %+v, %v, %v", a, created, err)
	}
	if err := service.ValidateHandle(a.Handle); err != nil {
		t.Fatalf("generated handle %q invalid: %v", a.Handle, err)
	}
	if a.DisplayName != a.Handle || !a.ProfilePublic || a.CreatedAt.IsZero() || !a.HandleChangedAt.IsZero() {
		t.Fatalf("new account = %+v", a)
	}
	again, created, err := e.svc.SignIn("google", "sub-1")
	if err != nil || created || again.ID != a.ID {
		t.Fatalf("second SignIn = %+v, %v, %v", again, created, err)
	}
	// Same user id at another provider is another person.
	other, created, _ := e.svc.SignIn("github", "sub-1")
	if !created || other.ID == a.ID {
		t.Fatal("providers share an identity namespace")
	}
	if _, _, err := e.svc.SignIn("", "x"); err == nil {
		t.Fatal("empty provider accepted")
	}
}

func TestSignInCapsAccounts(t *testing.T) {
	e := newAcctEnv(t, service.Options{MaxAccounts: 2})
	e.signIn(t, "a")
	e.signIn(t, "b")
	if _, _, err := e.svc.SignIn("google", "c"); err == nil {
		t.Fatal("third account accepted over the cap")
	} else {
		wantCode(t, err, service.CodeLimit)
	}
	if _, created, err := e.svc.SignIn("google", "a"); err != nil || created {
		t.Fatalf("existing account must still sign in at the cap: %v", err)
	}
}

func TestLoginLifecycle(t *testing.T) {
	e := newAcctEnv(t, service.Options{LoginTTL: time.Hour})
	a := e.signIn(t, "u1")

	tok1, exp, err := e.svc.StartLogin(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := e.clk.Now().Add(time.Hour); !exp.Equal(want) {
		t.Fatalf("expires %v, want %v", exp, want)
	}
	if len(tok1) < 40 {
		t.Fatalf("token too short: %d chars", len(tok1))
	}
	got, ok := e.svc.Authenticate(tok1)
	if !ok || got.ID != a.ID {
		t.Fatal("fresh token not accepted")
	}
	// Every login is a brand-new token (rotation: nothing is reused).
	tok2, _, _ := e.svc.StartLogin(a.ID)
	if tok2 == tok1 {
		t.Fatal("two logins share a token")
	}

	// Expiry is enforced from the stored time, not trusted from the client.
	e.clk.Advance(59 * time.Minute)
	if _, ok := e.svc.Authenticate(tok1); !ok {
		t.Fatal("token rejected before expiry")
	}
	e.clk.Advance(2 * time.Minute)
	if _, ok := e.svc.Authenticate(tok1); ok {
		t.Fatal("token accepted after expiry")
	}

	// Logout invalidates only that token.
	tok3, _, _ := e.svc.StartLogin(a.ID)
	tok4, _, _ := e.svc.StartLogin(a.ID)
	e.svc.EndLogin(tok3)
	if _, ok := e.svc.Authenticate(tok3); ok {
		t.Fatal("token valid after logout")
	}
	if _, ok := e.svc.Authenticate(tok4); !ok {
		t.Fatal("logging one browser out signed out another")
	}

	for _, bad := range []string{"", "short", "not-a-token-not-a-token-not-a-token", string(make([]byte, 500))} {
		if _, ok := e.svc.Authenticate(bad); ok {
			t.Errorf("Authenticate(%q) succeeded", bad)
		}
	}
	if _, _, err := e.svc.StartLogin("0123456789abcdef01234567"); err == nil {
		t.Fatal("StartLogin for an unknown account succeeded")
	}
}

func TestUpdateAccount(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	a := e.signIn(t, "u1")
	b := e.signIn(t, "u2")
	str := func(s string) *string { return &s }
	flag := func(v bool) *bool { return &v }

	t.Run("first handle change is free and mirrors the display name", func(t *testing.T) {
		got, err := e.svc.UpdateAccount(a.ID, service.AccountUpdate{Handle: str("  @Speedy_Fox ")})
		if err != nil {
			t.Fatal(err)
		}
		if got.Handle != "speedy_fox" || got.DisplayName != "speedy_fox" || got.HandleChangedAt.IsZero() {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("cooldown", func(t *testing.T) {
		_, err := e.svc.UpdateAccount(a.ID, service.AccountUpdate{Handle: str("another_one")})
		if err == nil {
			t.Fatal("second change inside the cooldown accepted")
		}
		wantCode(t, err, service.CodeLimit)
		at := e.svc.HandleChangeableAt(mustGet(t, e, a.ID))
		if want := e.clk.Now().Add(service.DefaultHandleCooldown); !at.Equal(want) {
			t.Fatalf("HandleChangeableAt = %v, want %v", at, want)
		}
		e.clk.Advance(service.DefaultHandleCooldown + time.Second)
		if _, err := e.svc.UpdateAccount(a.ID, service.AccountUpdate{Handle: str("another_one")}); err != nil {
			t.Fatalf("change after the cooldown: %v", err)
		}
	})
	t.Run("taken, case-insensitively", func(t *testing.T) {
		_, err := e.svc.UpdateAccount(b.ID, service.AccountUpdate{Handle: str("ANOTHER_ONE")})
		if err == nil {
			t.Fatal("took someone else's handle")
		}
		wantCode(t, err, service.CodeInvalidArgument)
	})
	t.Run("same handle is a no-op and does not start the cooldown", func(t *testing.T) {
		before := mustGet(t, e, b.ID)
		got, err := e.svc.UpdateAccount(b.ID, service.AccountUpdate{Handle: str(before.Handle)})
		if err != nil || !got.HandleChangedAt.IsZero() {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("invalid and blocked handles", func(t *testing.T) {
		for _, h := range []string{"ab", "has space", "admin", "shitlord", ""} {
			if _, err := e.svc.UpdateAccount(b.ID, service.AccountUpdate{Handle: str(h)}); err == nil {
				t.Errorf("handle %q accepted", h)
			}
		}
	})
	t.Run("display name and visibility", func(t *testing.T) {
		got, err := e.svc.UpdateAccount(b.ID, service.AccountUpdate{DisplayName: str("  Bee  Eater "), ProfilePublic: flag(false)})
		if err != nil || got.DisplayName != "Bee Eater" || got.ProfilePublic {
			t.Fatalf("got %+v, %v", got, err)
		}
		got, _ = e.svc.UpdateAccount(b.ID, service.AccountUpdate{DisplayName: str("")})
		if got.DisplayName != got.Handle {
			t.Fatalf("empty display name should fall back to the handle: %+v", got)
		}
		if _, err := e.svc.UpdateAccount(b.ID, service.AccountUpdate{DisplayName: str("f u c k")}); err == nil {
			t.Fatal("blocked display name accepted")
		}
	})
}

func mustGet(t *testing.T, e *acctEnv, id string) *service.Account {
	t.Helper()
	a, err := e.svc.GetAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOwnedSessionTokenIsHashedAtRest(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	a := e.signIn(t, "u1")
	s, token, err := e.svc.CreateOwnedSession("wide", "Zed", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || s.AccountID != a.ID {
		t.Fatalf("session = %+v token %q", s, token)
	}
	if s.OwnerHash != service.HashToken(token) || s.OwnerHash == token {
		t.Fatal("owner hash is not the SHA-256 of the token")
	}
	// A plain CreateSession keeps the old behaviour: no account, no token.
	g, err := e.svc.CreateSession("wide", "Guest")
	if err != nil || g.AccountID != "" || g.OwnerHash != "" {
		t.Fatalf("guest session = %+v, %v", g, err)
	}
	// An unknown account id is ignored rather than trusted.
	u, _, err := e.svc.CreateOwnedSession("wide", "X", "0123456789abcdef01234567")
	if err != nil || u.AccountID != "" {
		t.Fatalf("unknown account attached: %+v, %v", u, err)
	}
	// Each session gets its own token.
	_, token2, _ := e.svc.CreateOwnedSession("wide", "Zed", "")
	if token2 == token {
		t.Fatal("owner tokens repeat")
	}
}

func TestClaimSessions(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice, bob := e.signIn(t, "alice"), e.signIn(t, "bob")

	guest, guestTok, _ := e.svc.CreateOwnedSession("wide", "G", "")
	legacy, _ := e.svc.CreateSession("wide", "Old") // before owner tokens: nothing to claim with
	aliceOwn, aliceTok, _ := e.svc.CreateOwnedSession("wide", "A", alice.ID)

	tests := []struct {
		name    string
		by      string
		session string
		token   string
		want    string
	}{
		{"right token", alice.ID, guest.ID, guestTok, service.ClaimAttached},
		{"again by the same account", alice.ID, guest.ID, guestTok, service.ClaimAlreadyAttached},
		{"re-attach to another account is refused even with the token", bob.ID, guest.ID, guestTok, service.ClaimRejected},
		{"session created signed in, claimed by someone else", bob.ID, aliceOwn.ID, aliceTok, service.ClaimRejected},
		{"session id alone", bob.ID, legacy.ID, "", service.ClaimRejected},
		{"wrong token", bob.ID, legacy.ID, "wrong", service.ClaimRejected},
		{"legacy session with any token", bob.ID, legacy.ID, guestTok, service.ClaimRejected},
		{"unknown session", bob.ID, "00000000deadbeef", guestTok, service.ClaimRejected},
		{"another session's token", bob.ID, aliceOwn.ID, guestTok, service.ClaimRejected},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.svc.ClaimSessions(tc.by, []service.SessionClaim{{SessionID: tc.session, OwnerToken: tc.token}})
			if err != nil || len(res) != 1 {
				t.Fatalf("ClaimSessions = %v, %v", res, err)
			}
			if res[0].Status != tc.want || res[0].SessionID != tc.session {
				t.Fatalf("status = %s, want %s", res[0].Status, tc.want)
			}
		})
	}
	if got := e.get(t, guest.ID).AccountID; got != alice.ID {
		t.Fatalf("session owner = %q, want alice", got)
	}
	if got := e.get(t, legacy.ID).AccountID; got != "" {
		t.Fatalf("legacy session was claimed by %q", got)
	}
	// The claim reached storage.
	if p := e.pers.latest(guest.ID); p == nil || p.AccountID != alice.ID {
		t.Fatal("claim was not persisted")
	}

	t.Run("needs an account", func(t *testing.T) {
		if _, err := e.svc.ClaimSessions("0123456789abcdef01234567", nil); err == nil {
			t.Fatal("claim by an unknown account accepted")
		} else {
			wantCode(t, err, service.CodeUnauthenticated)
		}
	})
	t.Run("batch size is bounded", func(t *testing.T) {
		many := make([]service.SessionClaim, service.MaxClaims+1)
		if _, err := e.svc.ClaimSessions(alice.ID, many); err == nil {
			t.Fatal("oversized batch accepted")
		} else {
			wantCode(t, err, service.CodeInvalidArgument)
		}
	})
}

func TestLeaderboardCollapsesOneEntryPerAccount(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice, bob := e.signIn(t, "alice"), e.signIn(t, "bob")
	e.svc.UpdateAccount(alice.ID, service.AccountUpdate{DisplayName: ptr("Alice A")})

	// Alice wins three times (6, 7, 8 slots); Bob once (7); a guest once (7).
	idWorse, _ := e.ownedWin(t, alice.ID, "ignored1", wideWasteful())
	idBest, _ := e.ownedWin(t, alice.ID, "ignored2", wideViaSub())
	idMid, _ := e.ownedWin(t, alice.ID, "ignored3", wideDirect())
	bobID, _ := e.ownedWin(t, bob.ID, "bobsession", wideDirect())
	guestID := e.wonBy(t, "wide", "GuestG", wideDirect())
	_, _, _ = idWorse, idMid, bobID

	lb, err := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide", AccountID: alice.ID})
	if err != nil {
		t.Fatal(err)
	}
	if lb.Total != 3 || len(lb.Entries) != 3 {
		t.Fatalf("board = %+v, want 3 entries (alice once, bob, guest)", lb)
	}
	first := lb.Entries[0]
	if first.Rank != 1 || first.SessionID != idBest || first.Slots != 6 || first.DisplayName != "Alice A" || first.Handle != alice.Handle || !first.Mine {
		t.Fatalf("first = %+v", first)
	}
	if lb.Yours == nil || lb.Yours.SessionID != idBest {
		t.Fatalf("yours = %+v", lb.Yours)
	}
	byName := map[string]service.LeaderboardEntry{}
	for _, en := range lb.Entries {
		byName[en.DisplayName] = en
	}
	if g := byName["GuestG"]; g.SessionID != guestID || g.Handle != "" || g.Mine {
		t.Fatalf("guest entry = %+v", g)
	}
	if b := byName[bob.Handle]; b.Handle != bob.Handle || b.Mine {
		t.Fatalf("bob entry = %+v (display name defaults to the handle)", b)
	}

	// Bob sees his own entry flagged, not Alice's.
	lbBob, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide", AccountID: bob.ID})
	if lbBob.Yours == nil || lbBob.Yours.Handle != bob.Handle || !lbBob.Yours.Mine {
		t.Fatalf("bob yours = %+v", lbBob.Yours)
	}
	for _, en := range lbBob.Entries {
		if en.Mine != (en.Handle == bob.Handle) {
			t.Fatalf("Mine flag wrong: %+v", en)
		}
	}

	// A guest browser that only remembers one of alice's sessions still finds her entry.
	lbSess, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide", SessionID: idWorse})
	if lbSess.Yours == nil || lbSess.Yours.SessionID != idBest {
		t.Fatalf("yours via session = %+v", lbSess.Yours)
	}
	// ...and a guest session id still resolves to the guest's own row.
	lbG, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide", SessionID: guestID})
	if lbG.Yours == nil || lbG.Yours.SessionID != guestID {
		t.Fatalf("guest yours = %+v", lbG.Yours)
	}

	// The STEPS board collapses too, with each account's best under that order.
	lbSteps, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide", Order: service.OrderSteps})
	if lbSteps.Total != 3 {
		t.Fatalf("steps board total = %d, want 3", lbSteps.Total)
	}
	if lbSteps.Entries[0].Steps != 7 {
		t.Fatalf("steps board leader = %+v", lbSteps.Entries[0])
	}
}

func ptr[T any](v T) *T { return &v }

func TestPrivateProfileIsMaskedOnBoards(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice := e.signIn(t, "alice")
	e.svc.UpdateAccount(alice.ID, service.AccountUpdate{DisplayName: ptr("Secret Name"), ProfilePublic: ptr(false)})
	id, _ := e.ownedWin(t, alice.ID, "Secret Name", wideViaSub())
	e.ownedWin(t, alice.ID, "Secret Name", wideDirect())

	lb, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide"})
	if lb.Total != 1 { // still one entry per account, even when private
		t.Fatalf("total = %d", lb.Total)
	}
	en := lb.Entries[0]
	if en.DisplayName != service.PrivatePlayerName || en.Handle != "" || en.SessionID != id || en.Mine {
		t.Fatalf("private entry = %+v", en)
	}
	// The owner still recognises their own entry.
	own, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide", AccountID: alice.ID})
	if !own.Entries[0].Mine || own.Yours == nil {
		t.Fatalf("owner view = %+v", own)
	}
	// Neither the account's display name nor its handle leaks through any entry.
	runs, _ := e.svc.GetPlayerRuns(service.RunsQuery{SessionIDs: []string{id}})
	if len(runs) != 1 || runs[0].Entry.DisplayName != service.PrivatePlayerName || runs[0].Entry.Handle != "" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestProfileVisibility(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice, bob := e.signIn(t, "alice"), e.signIn(t, "bob")
	id, _ := e.ownedWin(t, alice.ID, "x", wideViaSub())
	e.wonBy(t, "wide", "GuestG", wideDirect())

	p, err := e.svc.GetProfile(alice.Handle, "")
	if err != nil || p.IsMine || len(p.Runs) != 1 || p.Runs[0].MapID != "wide" || p.Runs[0].Entry.SessionID != id || p.Runs[0].Entry.Rank != 1 || p.Runs[0].Total != 2 {
		t.Fatalf("public profile = %+v, %v", p, err)
	}
	if p, _ = e.svc.GetProfile("  "+alice.Handle+"  ", alice.ID); !p.IsMine {
		t.Fatal("owner not recognised")
	}
	if _, err := e.svc.GetProfile("nobodyhere", ""); err == nil {
		t.Fatal("unknown handle found")
	} else {
		wantCode(t, err, service.CodeNotFound)
	}

	e.svc.UpdateAccount(alice.ID, service.AccountUpdate{ProfilePublic: ptr(false)})
	for name, viewer := range map[string]string{"anonymous": "", "other player": bob.ID} {
		if _, err := e.svc.GetProfile(alice.Handle, viewer); err == nil {
			t.Errorf("private profile visible to %s", name)
		} else {
			wantCode(t, err, service.CodeNotFound)
		}
	}
	if p, err := e.svc.GetProfile(alice.Handle, alice.ID); err != nil || !p.IsMine {
		t.Fatalf("owner cannot see own private profile: %v", err)
	}
}

func TestPlayerRunsIncludeAccountAndStillMatchGuests(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice := e.signIn(t, "alice")
	e.ownedWin(t, alice.ID, "x", wideViaSub())
	guestID := e.wonBy(t, "corridor", "GuestG", corridorFast())

	// Signed in with this browser's guest session: both maps.
	runs, err := e.svc.GetPlayerRuns(service.RunsQuery{SessionIDs: []string{guestID}, AccountID: alice.ID})
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v", runs, err)
	}
	// Signed in with no remembered sessions: the account's runs alone.
	runs, err = e.svc.GetPlayerRuns(service.RunsQuery{AccountID: alice.ID})
	if err != nil || len(runs) != 1 || runs[0].MapID != "wide" || !runs[0].Entry.Mine {
		t.Fatalf("account-only runs = %+v, %v", runs, err)
	}
	// Signed out, guest session only.
	runs, _ = e.svc.GetPlayerRuns(service.RunsQuery{SessionIDs: []string{guestID}})
	if len(runs) != 1 || runs[0].MapID != "corridor" {
		t.Fatalf("guest runs = %+v", runs)
	}
	// By name matches guests only, never an account's display name.
	runs, _ = e.svc.GetPlayerRuns(service.RunsQuery{Name: alice.DisplayName})
	if len(runs) != 0 {
		t.Fatalf("name selector leaked an account: %+v", runs)
	}
	runs, _ = e.svc.GetPlayerRuns(service.RunsQuery{Name: "guestg"})
	if len(runs) != 1 {
		t.Fatalf("name selector lost the guest: %+v", runs)
	}
	// Selector rules: nothing, or name with ids, is still an error.
	if _, err := e.svc.GetPlayerRuns(service.RunsQuery{}); err == nil {
		t.Fatal("no selector accepted")
	}
	if _, err := e.svc.GetPlayerRuns(service.RunsQuery{Name: "x", SessionIDs: []string{"a"}}); err == nil {
		t.Fatal("name together with ids accepted")
	}
}

func TestDeleteAccountFallout(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice, bob := e.signIn(t, "alice"), e.signIn(t, "bob")
	e.svc.UpdateAccount(alice.ID, service.AccountUpdate{DisplayName: ptr("Alice Real Name")})
	tok, _, _ := e.svc.StartLogin(alice.ID)
	idA, _ := e.ownedWin(t, alice.ID, "TypedName", wideViaSub())
	idB, _ := e.ownedWin(t, bob.ID, "b", wideDirect())

	if err := e.svc.DeleteAccount(alice.ID); err != nil {
		t.Fatal(err)
	}
	// Account, logins and profile are gone.
	if _, err := e.svc.GetAccount(alice.ID); err == nil {
		t.Fatal("account survived")
	}
	if _, ok := e.svc.Authenticate(tok); ok {
		t.Fatal("login survived")
	}
	if _, err := e.svc.GetProfile(alice.Handle, ""); err == nil {
		t.Fatal("profile survived")
	}
	// Decision: her runs stay on the board as ordinary guest entries under a
	// fresh guest name; nothing links them to the account's handle or name.
	s := e.get(t, idA)
	if s.AccountID != "" || s.DisplayName == "TypedName" || s.DisplayName == "Alice Real Name" || s.DisplayName == "" {
		t.Fatalf("session after delete = account %q name %q", s.AccountID, s.DisplayName)
	}
	if p := e.pers.latest(idA); p == nil || p.AccountID != "" {
		t.Fatal("detached session was not persisted")
	}
	lb, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide"})
	if lb.Total != 2 {
		t.Fatalf("total = %d, want 2", lb.Total)
	}
	for _, en := range lb.Entries {
		if en.SessionID == idA && (en.Handle != "" || en.DisplayName != s.DisplayName) {
			t.Fatalf("entry after delete = %+v", en)
		}
	}
	// Bob is untouched.
	if e.get(t, idB).AccountID != bob.ID {
		t.Fatal("another account's session was detached")
	}
	// The handle is free again for a new sign-up.
	if _, err := e.svc.UpdateAccount(bob.ID, service.AccountUpdate{Handle: ptr(alice.Handle)}); err != nil {
		t.Fatalf("released handle not reusable: %v", err)
	}
	if err := e.svc.DeleteAccount(alice.ID); err == nil {
		t.Fatal("deleting twice succeeded")
	}
}

// A session whose account disappeared (a crash between detaching and deleting,
// or a restored backup) is simply a guest row.
func TestSessionWithMissingAccountCountsAsGuest(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice := e.signIn(t, "alice")
	id, _ := e.ownedWin(t, alice.ID, "Typed", wideDirect())
	if err := e.store.Delete(alice.ID); err != nil { // bypass the service: sessions keep the stale link
		t.Fatal(err)
	}
	lb, _ := e.svc.GetLeaderboard(service.LeaderboardQuery{MapID: "wide"})
	if lb.Total != 1 || lb.Entries[0].SessionID != id || lb.Entries[0].DisplayName != "Typed" || lb.Entries[0].Handle != "" {
		t.Fatalf("board = %+v", lb)
	}
}
