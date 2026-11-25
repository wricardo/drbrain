package graph

import (
	"context"
	"testing"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
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

func (f *fakeService) RenameSession(id, name string) (*service.Session, error) {
	s, err := f.GetSession(id)
	if err != nil {
		return nil, err
	}
	s.DisplayName = name
	if f.sessions != nil {
		f.sessions[id] = s
	}
	return s, nil
}

// TestAdminKeyArgumentGate covers the admin(key) argument path, which must use
// the same policy as the X-Admin-Key header.
func TestAdminKeyArgumentGate(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		adminKey  string
		allowOpen string
		wantCode  string
	}{
		{"no key configured, not opted out", "", "", "", service.CodeForbidden},
		{"no key configured, opted out", "", "", "true", ""},
		{"key configured, wrong key", "nope", "secret", "", service.CodeForbidden},
		{"key configured, correct key", "secret", "secret", "", ""},
		{"opt-out ignored when key configured", "", "secret", "true", service.CodeForbidden},
		{"key is prefix of configured", "secre", "secret", "", service.CodeForbidden},
		{"configured is prefix of key", "secret-and-more", "secret", "", service.CodeForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ADMIN_API_KEY", tc.adminKey)
			t.Setenv("ALLOW_UNAUTHENTICATED_ADMIN", tc.allowOpen)
			m := &mutationResolver{&Resolver{Service: &fakeService{}}}
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

// TestAdminSessionActions exercises delete (idempotent) and rename through the
// admin object, with no owner token and no signed-in viewer.
func TestAdminSessionActions(t *testing.T) {
	t.Setenv("ADMIN_API_KEY", "secret")
	fs := &fakeService{
		maps: map[string]*engine.Map{"m1": mustMap(t, "m1")},
		sessions: map[string]*service.Session{
			"s1": {ID: "s1", MapID: "m1", Map: mustMap(t, "s1"), DisplayName: "one"},
			"s2": {ID: "s2", MapID: "m1", Map: mustMap(t, "s2"), DisplayName: "old"},
		},
	}
	r := &Resolver{Service: fs}
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
	// A rejected owner check does not exist here: a missing id is success.
	if ok, err := admin.DeleteSession(ctx, nil, "ghost"); err != nil || !ok {
		t.Fatalf("delete missing = %v, %v", ok, err)
	}

	s, err := admin.RenameSession(ctx, nil, "s2", "new name")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if s == nil || s.DisplayName != "new name" {
		t.Fatalf("rename returned %+v", s)
	}
	if fs.sessions["s2"].DisplayName != "new name" {
		t.Fatal("rename did not persist to the session")
	}
}
