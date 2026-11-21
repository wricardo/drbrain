package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/wricardo/drbrain/games/tesla/game/service"
	"github.com/wricardo/drbrain/games/tesla/graph/model"
	"github.com/wricardo/drbrain/platform/auth"
)

// This file holds map ownership/authorization helpers. It is hand-written so
// gqlgen preserves it across regenerations.

// viewerID is the signed-in viewer's account id, "" for guests.
func viewerID(ctx context.Context) string {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account.ID
	}
	return ""
}

// requireViewer returns the signed-in account id or an UNAUTHENTICATED error.
// Map mutations require an account: an edit token only ever supplements
// ownership for legacy maps, it never replaces a login.
func (r *Resolver) requireViewer(ctx context.Context) (string, error) {
	id := viewerID(ctx)
	if id == "" {
		return "", codedGraphQLError("UNAUTHENTICATED", "sign in to manage maps")
	}
	return id, nil
}

// mapActor identifies who is changing a map: the signed-in account and whether
// the request carried valid admin credentials.
func (r *Resolver) mapActor(ctx context.Context, accountID string) service.MapActor {
	return service.MapActor{AccountID: accountID, Admin: r.adminAuthorized(ctx)}
}

// codedGraphQLError builds a gqlerror carrying extensions.code.
func codedGraphQLError(code, msg string) *gqlerror.Error {
	return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": code}}
}

// mapServiceError maps the service's map sentinels onto GraphQL error codes.
// Errors that already carry a code (gqlerror) pass through unchanged.
func mapServiceError(err error) error {
	if err == nil {
		return nil
	}
	var coded *gqlerror.Error
	if errors.As(err, &coded) {
		return err
	}
	code := ""
	switch {
	case errors.Is(err, service.ErrMapNotFound):
		code = "NOT_FOUND"
	case errors.Is(err, service.ErrMapForbidden),
		errors.Is(err, service.ErrMapReadOnly),
		errors.Is(err, service.ErrEditTokenRequired),
		errors.Is(err, service.ErrInvalidEditToken):
		code = "FORBIDDEN"
	case errors.Is(err, service.ErrMapExists),
		errors.Is(err, service.ErrMapInvalid):
		code = "INVALID_ARGUMENT"
	case errors.Is(err, service.ErrMapLimit):
		code = "LIMIT_REACHED"
	}
	if code == "" {
		return err
	}
	return codedGraphQLError(code, err.Error())
}

// mapStatus converts a service lifecycle state to the GraphQL enum.
func mapStatus(status service.MapStatus) model.MapStatus {
	if status == service.MapStatusDraft {
		return model.MapStatusDraft
	}
	return model.MapStatusPublished
}

// fillMapMeta populates the viewer-dependent fields of a GameMap from its
// catalogue entry: shipped flag, lifecycle state and ownership.
func fillMapMeta(obj *model.GameMap, entry *service.MapEntry, viewer string) {
	if obj == nil || entry == nil {
		return
	}
	obj.Builtin = entry.Builtin
	obj.Status = mapStatus(entry.Status)
	obj.OwnedByMe = viewer != "" && entry.OwnerID == viewer
}

// entryVisible reports whether viewer may read this entry: shipped and
// published maps are public, drafts belong to their owner alone.
func entryVisible(entry *service.MapEntry, viewer string) bool {
	if entry == nil {
		return false
	}
	if entry.Builtin || entry.Status == service.MapStatusPublished {
		return true
	}
	return viewer != "" && entry.OwnerID == viewer
}

// authorizeMapEntry enforces the map write policy and returns the actor to use:
//   - an admin key may change any map, including shipped ones;
//   - an owned community map may be changed only by its owner (edit tokens
//     never bypass account ownership once a map has an owner);
//   - a legacy unowned custom map may be changed by a signed-in player who
//     presents the edit token it was created with, until claimMap attaches it.
//     The verified token stands in for the admin key for that one row only.
//   - shipped maps otherwise require the admin key.
func (r *Resolver) authorizeMapEntry(ctx context.Context, entry *service.MapEntry, actor service.MapActor, editKey *string) (service.MapActor, error) {
	if actor.Admin {
		return actor, nil
	}
	if entry.Builtin {
		return actor, codedGraphQLError("FORBIDDEN", "shipped maps can only be modified with an admin key")
	}
	if entry.OwnerID != "" {
		if entry.OwnerID != actor.AccountID {
			return actor, codedGraphQLError("FORBIDDEN", "only the map's owner may change it")
		}
		return actor, nil
	}
	// Legacy ownerless row: the edit token is the only credential until claim.
	key := ""
	if editKey != nil {
		key = strings.TrimSpace(*editKey)
	}
	if key == "" {
		return actor, codedGraphQLError("FORBIDDEN", "this legacy map needs its edit key or an admin key")
	}
	if err := r.Service.VerifyMapEditToken(ctx, entry.Name, key); err != nil {
		return actor, codedGraphQLError("FORBIDDEN", err.Error())
	}
	actor.Admin = true
	return actor, nil
}

// sessionVisible reports whether viewer may read a session. A session on a
// DRAFT is visible only to that map's owner. When the map no longer exists, the
// session is visible only to a non-empty session owner matching the viewer, so
// a deleted draft never leaks through its snapshot; guest and legacy sessions
// without an owner stay hidden.
func (r *Resolver) sessionVisible(ctx context.Context, s *service.SessionInfo) bool {
	if s == nil {
		return false
	}
	viewer := viewerID(ctx)
	entry, err := r.Service.GetMapEntry(ctx, s.MapName)
	if err != nil {
		return s.OwnerAccountID != "" && s.OwnerAccountID == viewer
	}
	if entry.Status != service.MapStatusDraft {
		return true
	}
	return entry.OwnerID != "" && entry.OwnerID == viewer
}

// visibleSession loads a session and hides it from callers who may not see its
// map: the answer is NOT_FOUND, exactly like an unknown session id.
func (r *Resolver) visibleSession(ctx context.Context, id string) (*service.SessionInfo, error) {
	s, err := r.Service.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !r.sessionVisible(ctx, s) {
		return nil, codedGraphQLError("NOT_FOUND", fmt.Sprintf("session %q not found", id))
	}
	return s, nil
}
