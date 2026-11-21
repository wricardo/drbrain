package graph

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/mapstore"
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

// requireViewer returns the signed-in account or an UNAUTHENTICATED error. The
// message keeps the historical "unauthenticated:" prefix while the machine
// code travels in extensions.
func requireViewer(ctx context.Context) (*account.Account, error) {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account, nil
	}
	return nil, prefixedCodedErr("unauthenticated", codeUnauthenticated, "sign in first")
}

// adminAuthorized reports whether the request carries the delivery admin key.
// With no key configured, the explicit local-development opt-out is the only
// way to authorize an admin operation.
func (r *Resolver) adminAuthorized(ctx context.Context) bool {
	if r.AdminKey == "" {
		return r.AllowUnauthenticatedAdmin
	}
	req := auth.RequestFrom(ctx)
	if req == nil {
		return false
	}
	// Compare fixed-length digests so the configured key's length is not
	// leaked through timing.
	got := sha256.Sum256([]byte(req.Header.Get("X-Admin-Key")))
	want := sha256.Sum256([]byte(r.AdminKey))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// requireAdmin returns a FORBIDDEN error unless adminAuthorized. Sign-in is
// checked separately by every city mutation that uses it.
func (r *Resolver) requireAdmin(ctx context.Context) error {
	if r.adminAuthorized(ctx) {
		return nil
	}
	if r.AdminKey == "" && !r.AllowUnauthenticatedAdmin {
		return codedErr(codeForbidden, "admin operation disabled: set DELIVERY_ADMIN_API_KEY and send X-Admin-Key")
	}
	return codedErr(codeForbidden, "admin operation requires a valid X-Admin-Key")
}

// sessionCityVisible reports whether this viewer may read a session whose city
// is cityID.
//
// A session snapshots its city, but the city's ownership still controls who may
// see the session: a DRAFT is private to its owner, so a guest (or another
// player) must not learn its rows through the session. A published or builtin
// city is public. A city that no longer exists leaves the old load behaviour
// alone: rehydration reports the city error, while a still-live session stays
// readable.
func (r *Resolver) sessionCityVisible(ctx context.Context, cityID string) (bool, error) {
	if cityID == "" || r.Catalog == nil {
		return true, nil
	}
	entry, err := r.Catalog.Lookup(cityID)
	if err != nil {
		if errors.Is(err, cityconfig.ErrNotFound) {
			return true, nil
		}
		return false, cityCodedError(err)
	}
	if entry.Status != mapstore.StatusDraft {
		return true, nil
	}
	viewer := viewerID(ctx)
	return entry.OwnerID != "" && entry.OwnerID == viewer, nil
}

// authorize loads a session and checks that this caller may change it. A
// missing session is not_found: regardless of the credential, so an id alone
// never confirms whether a save exists to an unauthorized caller. A present
// session the caller cannot prove ownership of is forbidden:.
func (r *Resolver) authorize(ctx context.Context, id string, ownerToken *string) (*session.Session, error) {
	sess, err := r.loadRaw(id)
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
