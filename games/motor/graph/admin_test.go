package graph

import (
	"context"
	"testing"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
)

// DeleteSession is the admin session delete: NOT_FOUND when the id exists in
// neither memory nor storage, so the resolver can make it idempotent.
func (f *fakeService) DeleteSession(id string) error {
	if f.err != nil {
		return f.err
	}
	if _, ok := f.sessions[id]; !ok {
		return service.Errorf(service.CodeNotFound, "session %q not found", id)
	}
	delete(f.sessions, id)
	return nil
}

func (f *fakeService) Pause(id string) (*service.Session, error) {
	s, err := f.GetSession(id)
	if err != nil {
		return nil, err
	}
	s.Playing = false
	if f.sessions != nil {
		f.sessions[id] = s
	}
	return s, nil
}

// TestAdminKeyArgumentGate covers the admin(key) argument path against the
// Resolver.AdminPolicy the header gate uses.
func TestAdminKeyArgumentGate(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		adminKey  string
		allowOpen bool
		wantCode  string
	}{
		{"no key configured, not opted out", "", "", false, service.CodeForbidden},
		{"no key configured, opted out", "", "", true, ""},
		{"key configured, wrong key", "nope", "secret", false, service.CodeForbidden},
		{"key configured, correct key", "secret", "secret", false, ""},
		{"opt-out ignored when key configured", "", "secret", true, service.CodeForbidden},
		{"key is prefix of configured", "secre", "secret", false, service.CodeForbidden},
		{"configured is prefix of key", "secret-and-more", "secret", false, service.CodeForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Resolver{AdminPolicy: Admin{Key: tc.adminKey, AllowUnauthenticated: tc.allowOpen}}
			m := &mutationResolver{r}
			_, err := m.Admin(context.Background(), tc.key)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("admin: unexpected error %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("admin: expected %s error, got none", tc.wantCode)
			}
			if got := codeOf(t, err); got != tc.wantCode {
				t.Fatalf("admin: code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

// TestAdminSessionActions exercises delete (idempotent) and pause through the
// admin object, with no owner token and no signed-in viewer.
func TestAdminSessionActions(t *testing.T) {
	fs := &fakeService{
		maps: map[string]*engine.Map{"m1": mustMap(t, "m1")},
		sessions: map[string]*service.Session{
			"s1": {ID: "s1", MapID: "m1", Playing: true},
			"s2": {ID: "s2", MapID: "m1", Playing: true},
		},
	}
	r := &Resolver{Service: fs, AdminPolicy: Admin{Key: "secret"}}
	ctx := context.Background()

	if _, err := (&mutationResolver{r}).Admin(ctx, "secret"); err != nil {
		t.Fatalf("admin: %v", err)
	}
	admin := &adminResolver{r}

	if ok, err := admin.DeleteSession(ctx, nil, "s1"); err != nil || !ok {
		t.Fatalf("delete existing = %v, %v", ok, err)
	}
	if _, ok := fs.sessions["s1"]; ok {
		t.Fatal("session s1 still present after admin delete")
	}
	if ok, err := admin.DeleteSession(ctx, nil, "ghost"); err != nil || !ok {
		t.Fatalf("delete missing = %v, %v", ok, err)
	}

	s, err := admin.PauseSession(ctx, nil, "s2")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if s == nil || s.Playing {
		t.Fatalf("pause returned %+v", s)
	}
	if fs.sessions["s2"].Playing {
		t.Fatal("pause did not stop the session")
	}
	// Pausing a missing session is NOT_FOUND, unlike delete's idempotency.
	if _, err := admin.PauseSession(ctx, nil, "ghost"); err == nil || codeOf(t, err) != service.CodeNotFound {
		t.Fatalf("pause missing error = %v", err)
	}
}
