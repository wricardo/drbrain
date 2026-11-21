package graph

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"

	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/games/mazes/graph/model"
	"github.com/wricardo/drbrain/platform/auth"
)

// This file holds the ownership/authorization helpers. They live outside
// schema.resolvers.go because gqlgen rewrites that file on every generate.

// viewerID is the signed-in viewer's account id, "" for guests.
func viewerID(ctx context.Context) string {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account.ID
	}
	return ""
}

// requireViewer returns the signed-in account id or an UNAUTHENTICATED error.
// Map management requires an account; map reads stay public.
func requireViewer(ctx context.Context) (string, error) {
	id := viewerID(ctx)
	if id == "" {
		return "", codedError("UNAUTHENTICATED", "sign in to manage maps")
	}
	return id, nil
}

// adminAuthorized reports whether the current request carries valid admin
// credentials (X-Admin-Key matches ADMIN_API_KEY, or the local-dev bypass).
// Unlike checkAdminKey it never errors, so callers can use it as an override.
func adminAuthorized(ctx context.Context) bool {
	required := os.Getenv("ADMIN_API_KEY")
	if required == "" {
		return os.Getenv("ALLOW_UNAUTHENTICATED_ADMIN") == "true"
	}
	r, _ := ctx.Value(HTTPRequestKey{}).(*http.Request)
	if r == nil {
		return false
	}
	got := sha256.Sum256([]byte(r.Header.Get("X-Admin-Key")))
	want := sha256.Sum256([]byte(required))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// mapActor identifies who is changing a map.
func mapActor(ctx context.Context, accountID string) service.MapActor {
	return service.MapActor{AccountID: accountID, Admin: adminAuthorized(ctx)}
}

// entryVisible reports whether viewer may read this entry: shipped and
// published maps are public, another player's draft is not.
func entryVisible(entry *service.MapEntry, viewer string) bool {
	if entry == nil {
		return false
	}
	if entry.Builtin || entry.Status == service.MapStatusPublished {
		return true
	}
	return viewer != "" && entry.OwnerID == viewer
}

// actor is who asks to change a session: the signed-in account of the request
// (if any) and the owner token they passed (if any).
func actor(ctx context.Context, ownerToken *string) service.Actor {
	a := service.Actor{AccountID: viewerID(ctx)}
	if ownerToken != nil {
		a.OwnerToken = *ownerToken
	}
	return a
}

// authorize loads a session and checks that this caller may change it. A
// missing session is NOT_FOUND regardless of the credential, so an id alone
// never confirms whether a session exists to an unauthorized caller. A present
// session the caller cannot prove ownership of is FORBIDDEN.
func (r *Resolver) authorize(ctx context.Context, id string, ownerToken *string) (*service.Session, error) {
	sess, err := r.Service.GetSession(id)
	if err != nil {
		return nil, err
	}
	if !sess.CanPlay(actor(ctx, ownerToken)) {
		return nil, service.Errorf(service.CodeForbidden,
			"this session belongs to whoever created it; pass its owner token or sign in to the owning account")
	}
	return sess, nil
}

// canPlay answers canPlaySession without ever erroring on a bad credential.
func (r *Resolver) canPlay(ctx context.Context, id string, ownerToken *string) (bool, error) {
	sess, err := r.Service.GetSession(id)
	if err != nil {
		var coded *service.Error
		if errors.As(err, &coded) && coded.Code == service.CodeNotFound {
			return false, nil
		}
		return false, err
	}
	return sess.CanPlay(actor(ctx, ownerToken)), nil
}

// sessionView converts a session snapshot and fills the embedded map's
// lifecycle fields, which depend on the viewer and are not part of the
// session snapshot itself.
func (r *Resolver) sessionView(ctx context.Context, s *service.Session) *model.Session {
	out := toSession(s)
	if out == nil || out.Map == nil || s == nil {
		return out
	}
	if entry, err := r.Service.GetMapEntry(s.MapID); err == nil {
		fillMapMeta(out.Map, entry, viewerID(ctx))
	} else {
		out.Map.Status = model.MapStatusPublished
	}
	return out
}

// sessionUpdateView is sessionView for a subscription update.
func (r *Resolver) sessionUpdateView(ctx context.Context, u *service.Session) *model.SessionUpdate {
	upd := toSessionUpdate(u)
	if upd != nil {
		upd.Session = r.sessionView(ctx, u)
	}
	return upd
}

// authorizeMapEntry enforces the map write policy: an admin key may change any
// map, an owned community map only its owner, and a legacy unowned map needs
// the admin key (mazes never issued edit tokens).
func authorizeMapEntry(_ context.Context, entry *service.MapEntry, actor service.MapActor) error {
	if actor.Admin {
		return nil
	}
	if entry.Builtin {
		return codedError(service.CodeForbidden, "shipped maps can only be modified with an admin key")
	}
	if entry.OwnerID == "" {
		return codedError(service.CodeForbidden, "this legacy map needs an admin key")
	}
	if entry.OwnerID != actor.AccountID {
		return codedError(service.CodeForbidden, "only the map's owner may change it")
	}
	return nil
}

// sessionVisible reports whether viewer may read a session. A session on
// another player's DRAFT is hidden. When the map no longer exists the session
// is visible only to a non-empty session owner matching the viewer, so a
// deleted draft never leaks through its snapshot; guest and legacy sessions
// without an owner stay hidden.
func (r *Resolver) sessionVisible(ctx context.Context, s *service.Session) bool {
	if s == nil {
		return false
	}
	viewer := viewerID(ctx)
	entry, err := r.Service.GetMapEntry(s.MapID)
	if err != nil {
		return s.OwnerAccountID != "" && s.OwnerAccountID == viewer
	}
	if entry.Status != service.MapStatusDraft {
		return true
	}
	return entry.OwnerID != "" && entry.OwnerID == viewer
}

// visibleSession loads a session and reports whether its map lets this caller
// read it.
func (r *Resolver) visibleSession(ctx context.Context, id string) (*service.Session, error) {
	s, err := r.Service.GetSession(id)
	if err != nil {
		return nil, err
	}
	if !r.sessionVisible(ctx, s) {
		return nil, service.Errorf(service.CodeNotFound, "session %q not found", id)
	}
	return s, nil
}
