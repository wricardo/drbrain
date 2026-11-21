package graph

import (
	"context"
	"testing"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
)

func (f *fakeService) Run(id string, _ int) (*service.Session, error) {
	return f.GetSession(id)
}

func (f *fakeService) ListSessions(service.SessionFilter) ([]*service.Session, error) {
	out := make([]*service.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s.Clone())
	}
	return out, nil
}

// Sessions on another player's DRAFT map must be absent/NOT_FOUND from every
// session-returning resolver, while the map owner still sees them.
func TestDraftSessionHiddenFromSessionResolvers(t *testing.T) {
	m := mustMap(t, "draft")
	fs := &fakeService{
		maps:     map[string]*engine.Map{"draft": m},
		meta:     map[string]service.MapEntry{"draft": {OwnerID: "alice", Status: engine.MapStatusDraft}},
		sessions: map[string]*service.Session{"s1": {ID: "s1", MapID: "draft", Map: m}},
	}
	hub := websocket.NewHub()
	r := NewResolver(fs, hub)
	ctx := context.Background()

	// Guest: detail null, list omits it, mutations and subscription reject it.
	s, err := r.Query().Session(ctx, "s1", nil)
	if err != nil || s != nil {
		t.Fatalf("guest session query = %+v, %v", s, err)
	}
	list, err := r.Query().Sessions(ctx, nil, nil, nil)
	if err != nil || len(list) != 0 {
		t.Fatalf("guest session list = %d rows, %v", len(list), err)
	}
	if _, err := r.Mutation().Run(ctx, "s1", nil, nil); codeOf(t, err) != service.CodeNotFound {
		t.Fatalf("guest run = %v", err)
	}
	if _, err := r.Mutation().DeleteSession(ctx, "s1", nil); codeOf(t, err) != service.CodeNotFound {
		t.Fatalf("guest delete = %v", err)
	}
	if _, err := r.Subscription().SessionUpdated(ctx, "s1", nil); codeOf(t, err) != service.CodeNotFound {
		t.Fatalf("guest subscription = %v", err)
	}

	// The draft owner still sees the session and can drive playback.
	owner := auth.WithViewer(ctx, &auth.Viewer{Account: &account.Account{ID: "alice"}})
	s, err = r.Query().Session(owner, "s1", nil)
	if err != nil || s == nil {
		t.Fatalf("owner session query = %+v, %v", s, err)
	}
	list, err = r.Query().Sessions(owner, nil, nil, nil)
	if err != nil || len(list) != 1 {
		t.Fatalf("owner session list = %d rows, %v", len(list), err)
	}
	if _, err := r.Mutation().Run(owner, "s1", nil, nil); err != nil {
		t.Fatalf("owner run: %v", err)
	}
}
