package graph

import (
	"context"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
	"github.com/wricardo/drbrain/platform/account"
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

// actor is who asks to change a session: the signed-in account of the request
// (if any) and the owner token they passed (if any).
func actor(ctx context.Context, ownerToken *string) session.Actor {
	a := session.Actor{AccountID: viewerID(ctx)}
	if ownerToken != nil {
		a.OwnerToken = *ownerToken
	}
	return a
}

// requireViewer returns the signed-in account or an unauthenticated: error.
func requireViewer(ctx context.Context) (*account.Account, error) {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account, nil
	}
	return nil, gqlErr("unauthenticated", "sign in first")
}

// authorize loads a session and checks that this caller may change it. A
// missing session is not_found: regardless of the credential, so an id alone
// never confirms whether a save exists to an unauthorized caller. A present
// session the caller cannot prove ownership of is forbidden:.
func (r *Resolver) authorize(ctx context.Context, id string, ownerToken *string) (*session.Session, error) {
	sess, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if !sess.CanPlay(actor(ctx, ownerToken)) {
		return nil, errForbidden("this save belongs to whoever created it; pass its owner token or sign in to the owning account")
	}
	return sess, nil
}

// summaryView is one list row with its owner resolved for ctx's viewer. A
// session with no account is "Guest"; a private profile is masked exactly as
// motor's boards do (account.PrivatePlayerName, no handle); a link to an
// account that no longer exists is treated as a guest row.
func (r *Resolver) summaryView(ctx context.Context, s session.Summary) *model.SessionSummary {
	out := summaryFrom(s)
	out.OwnerDisplayName = "Guest"
	if s.AccountID == "" || r.Accounts == nil {
		return out
	}
	a, ok := r.Accounts.Lookup(s.AccountID)
	if !ok {
		return out
	}
	viewer := viewerID(ctx)
	out.Mine = viewer != "" && viewer == a.ID
	if !a.ProfilePublic {
		out.OwnerDisplayName = account.PrivatePlayerName
		return out
	}
	out.OwnerDisplayName = a.DisplayName
	h := a.Handle
	out.OwnerHandle = &h
	return out
}
