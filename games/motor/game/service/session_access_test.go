package service_test

import (
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/service"
)

func TestAuthorizeSessionChange(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice, bob := e.signIn(t, "alice"), e.signIn(t, "bob")

	guest, guestTok, _ := e.svc.CreateOwnedSession("wide", "G", "")
	legacy, _ := e.svc.CreateSession("wide", "Old") // no owner token at all
	owned, ownedTok, _ := e.svc.CreateOwnedSession("wide", "A", alice.ID)
	claimed, claimedTok, _ := e.svc.CreateOwnedSession("wide", "C", "")
	if res, _ := e.svc.ClaimSessions(alice.ID, []service.SessionClaim{{SessionID: claimed.ID, OwnerToken: claimedTok}}); res[0].Status != service.ClaimAttached {
		t.Fatalf("claim = %v", res)
	}

	tests := []struct {
		name    string
		session string
		actor   service.SessionActor
		want    string // "" = allowed, else the error code
	}{
		// Guest sessions are open to anyone who knows the id, as they always were.
		{"guest session, nobody", guest.ID, service.SessionActor{}, ""},
		{"guest session, any account", guest.ID, service.SessionActor{AccountID: bob.ID}, ""},
		{"guest session without owner token", legacy.ID, service.SessionActor{}, ""},
		{"guest session, wrong token", guest.ID, service.SessionActor{OwnerToken: "nope"}, ""},
		{"guest session, right token", guest.ID, service.SessionActor{OwnerToken: guestTok}, ""},

		// A signed-in player's session: the owner, or the secret token, and nobody else.
		{"owned, nobody", owned.ID, service.SessionActor{}, service.CodeForbidden},
		{"owned, another account", owned.ID, service.SessionActor{AccountID: bob.ID}, service.CodeForbidden},
		{"owned, wrong token", owned.ID, service.SessionActor{OwnerToken: "wrong-token"}, service.CodeForbidden},
		{"owned, another session's token", owned.ID, service.SessionActor{OwnerToken: guestTok}, service.CodeForbidden},
		{"owned, empty token", owned.ID, service.SessionActor{OwnerToken: ""}, service.CodeForbidden},
		{"owned, the owner", owned.ID, service.SessionActor{AccountID: alice.ID}, ""},
		{"owned, the owner's token", owned.ID, service.SessionActor{OwnerToken: ownedTok}, ""},
		{"owned, other account but right token", owned.ID, service.SessionActor{AccountID: bob.ID, OwnerToken: ownedTok}, ""},

		// A session claimed later becomes protected from that moment.
		{"claimed, nobody", claimed.ID, service.SessionActor{}, service.CodeForbidden},
		{"claimed, the owner", claimed.ID, service.SessionActor{AccountID: alice.ID}, ""},
		{"claimed, its token", claimed.ID, service.SessionActor{OwnerToken: claimedTok}, ""},

		{"unknown session", "00000000deadbeef", service.SessionActor{}, service.CodeNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := e.svc.AuthorizeSessionChange(tc.session, tc.actor)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("allowed")
			}
			wantCode(t, err, tc.want)
		})
	}
}

// Playing a session stays open (the "Play with an AI" flow), and the check does
// not change anything by itself.
func TestOwnedSessionsCanStillBePlayedByAnyone(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice := e.signIn(t, "alice")
	s, _, _ := e.svc.CreateOwnedSession("wide", "A", alice.ID)
	if _, err := e.svc.SetProgram(s.ID, wideDirect()); err != nil {
		t.Fatalf("SetProgram by an agent: %v", err)
	}
	if _, err := e.svc.Step(s.ID); err != nil {
		t.Fatalf("Step by an agent: %v", err)
	}
	if _, err := e.svc.Reset(s.ID); err != nil {
		t.Fatalf("Reset by an agent: %v", err)
	}
}

// A link to an account that is gone, or accounts being switched off, leaves an
// ordinary guest session.
func TestAuthorizeSessionChangeIgnoresDeadAccountLinks(t *testing.T) {
	e := newAcctEnv(t, service.Options{})
	alice := e.signIn(t, "alice")
	s, _, _ := e.svc.CreateOwnedSession("wide", "A", alice.ID)
	if err := e.store.Delete(alice.ID); err != nil { // bypass the service: the session keeps the stale link
		t.Fatal(err)
	}
	if err := e.svc.AuthorizeSessionChange(s.ID, service.SessionActor{}); err != nil {
		t.Fatalf("stale link still protects the session: %v", err)
	}

	off := newEnv(t, envOpts{}, wideCfg)
	g, _ := off.svc.CreateSession("wide", "G")
	if err := off.svc.AuthorizeSessionChange(g.ID, service.SessionActor{}); err != nil {
		t.Fatalf("accounts off: %v", err)
	}
}
