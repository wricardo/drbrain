package service_test

import (
	"errors"
	"testing"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
)

// A session's visibility follows its map: another player's DRAFT (or an
// ownerless draft) is private; publishing makes it public; a deleted map leaves
// only the session's owning account able to see it.
func TestSessionVisibilityFollowsMapStatus(t *testing.T) {
	e := newEnv(t, envOpts{})
	alice, bob := "alice-id", "bob-id"

	e.maps.mu.Lock()
	e.maps.meta["corridor"] = service.MapEntry{OwnerID: alice, Status: engine.MapStatusDraft}
	e.maps.mu.Unlock()

	if _, err := e.svc.CreateSession("corridor"); err == nil {
		t.Fatal("guest created a session on another player's draft")
	} else {
		var se *service.Error
		if !errors.As(err, &se) || se.Code != service.CodeNotFound {
			t.Fatalf("guest create = %v, want NOT_FOUND", err)
		}
	}
	s, _, err := e.svc.CreateOwnedSession("corridor", alice)
	if err != nil {
		t.Fatalf("owner create: %v", err)
	}
	if _, _, err := e.svc.CreateOwnedSession("corridor", bob); err == nil {
		t.Fatal("another player created a session on the draft")
	}
	if !e.svc.SessionVisibleTo(s, alice) {
		t.Error("owner cannot see their draft session")
	}
	for _, viewer := range []string{"", bob} {
		if e.svc.SessionVisibleTo(s, viewer) {
			t.Errorf("viewer %q can see another player's draft session", viewer)
		}
	}

	// Publishing the draft makes its sessions public.
	e.maps.mu.Lock()
	e.maps.meta["corridor"] = service.MapEntry{OwnerID: alice, Status: engine.MapStatusPublished}
	e.maps.mu.Unlock()
	for _, viewer := range []string{"", bob} {
		if !e.svc.SessionVisibleTo(s, viewer) {
			t.Errorf("published map session hidden from %q", viewer)
		}
	}

	// A deleted map leaves the session visible only to its owning account.
	e.maps.mu.Lock()
	delete(e.maps.m, "corridor")
	delete(e.maps.meta, "corridor")
	e.maps.mu.Unlock()
	if !e.svc.SessionVisibleTo(s, alice) {
		t.Error("owner lost their session after the map was deleted")
	}
	for _, viewer := range []string{"", bob} {
		if e.svc.SessionVisibleTo(s, viewer) {
			t.Errorf("deleted-map session visible to %q", viewer)
		}
	}
}

func TestOwnerlessDraftIsNotPlayableByGuest(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.maps.mu.Lock()
	e.maps.meta["corridor"] = service.MapEntry{Status: engine.MapStatusDraft}
	e.maps.mu.Unlock()

	if _, err := e.svc.CreateSession("corridor"); err == nil {
		t.Fatal("guest created a session on an ownerless draft")
	}
	entry, err := e.maps.Entry("corridor")
	if err != nil {
		t.Fatal(err)
	}
	if entry.VisibleTo("") || entry.VisibleTo("anyone") {
		t.Fatal("ownerless draft is visible to a viewer")
	}
}
