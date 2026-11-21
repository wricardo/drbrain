// Package graph implements the GraphQL API (gqlgen resolvers, subscription
// forwarding and conversion from service types to schema models).
package graph

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/auth"
)

// CodeInternal is the extensions.code for unexpected server failures.
const CodeInternal = "INTERNAL"

// subscriptionBuffer is the output channel size of the subscription forwarder.
const subscriptionBuffer = 8

// Resolver is the gqlgen dependency injection root.
type Resolver struct {
	Service service.GameService
	Hub     *websocket.Hub
	// Accounts is the global account service (nil keeps the game guest-only).
	Accounts *account.Service
	// Admin gates writes to shipped content.
	Admin Admin
}

// Admin is the built-in map gate (ADMIN_API_KEY and
// ALLOW_UNAUTHENTICATED_ADMIN), parsed once at startup.
type Admin struct {
	// Key is ADMIN_API_KEY. When set, a write must carry it as X-Admin-Key.
	Key string
	// AllowUnauthenticated permits those writes while no key is configured
	// (local development only; ignored once Key is set).
	AllowUnauthenticated bool
}

// NewResolver builds the resolver root from the game service and the
// subscription hub.
func NewResolver(svc service.GameService, hub *websocket.Hub) *Resolver {
	return &Resolver{Service: svc, Hub: hub}
}

// HTTPRequestKey is the context key under which the *http.Request is stored
// by the HTTP middleware, so resolvers can read headers (X-Admin-Key).
type HTTPRequestKey struct{}

// viewerID is the viewer's account id, "" for guests.
func viewerID(ctx context.Context) string {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account.ID
	}
	return ""
}

// requireVisibleSession returns the session only when the viewer may see its
// map. A session on another player's DRAFT is NOT_FOUND, before any mutation.
func (r *Resolver) requireVisibleSession(ctx context.Context, id string) (*service.Session, error) {
	s, err := r.Service.GetSession(id)
	if err != nil {
		return nil, err
	}
	if !r.Service.SessionVisibleTo(s, viewerID(ctx)) {
		return nil, service.Errorf(service.CodeNotFound, "session %q not found", id)
	}
	return s, nil
}

// sessionActor is who is asking to change a session: the signed-in account of
// the request (if any) and the owner token they passed (if any).
func sessionActor(ctx context.Context, ownerToken *string) service.SessionActor {
	a := service.SessionActor{AccountID: viewerID(ctx)}
	if ownerToken != nil {
		a.OwnerToken = *ownerToken
	}
	return a
}

// mayEdit reports whether the caller may write a session's program (setProgram)
// and so may read it: the owner token holder, or the owning account's login. It
// reuses the same service rule the SetProgram mutation enforces, so a read can
// never be more permissive than a write. Spectators control playback instead
// (run, step, pause, reset), which never returns the program.
func (r *Resolver) mayEdit(ctx context.Context, id string, ownerToken *string) bool {
	return r.Service.AuthorizeSessionEdit(id, sessionActor(ctx, ownerToken)) == nil
}

// requireViewer returns the signed-in account or an UNAUTHENTICATED error.
func requireViewer(ctx context.Context) (*account.Account, error) {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account, nil
	}
	return nil, codedError(service.CodeUnauthenticated, "sign in first")
}

// checkAdminKey gates operations that change shipped content. ADMIN_API_KEY
// must be configured and sent as X-Admin-Key; local development can opt out
// explicitly with ALLOW_UNAUTHENTICATED_ADMIN=true (only honoured while no key
// is configured).
func (r *Resolver) checkAdminKey(ctx context.Context) error {
	if r.Admin.Key == "" {
		if r.Admin.AllowUnauthenticated {
			return nil
		}
		return codedError(service.CodeForbidden, "admin operation disabled: set ADMIN_API_KEY and send X-Admin-Key")
	}
	req, _ := ctx.Value(HTTPRequestKey{}).(*http.Request)
	if req == nil {
		return codedError(service.CodeForbidden, "admin operation requires X-Admin-Key header")
	}
	// Compare fixed-length digests so the key length is not leaked.
	got := sha256.Sum256([]byte(req.Header.Get("X-Admin-Key")))
	want := sha256.Sum256([]byte(r.Admin.Key))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return codedError(service.CodeForbidden, "forbidden: invalid or missing X-Admin-Key")
	}
	return nil
}

// mapActorFor resolves who is changing an existing map. Every map operation
// needs a signed-in player. A shipped map, or a legacy community map with no
// owner, also needs the admin key; an owned community map is down to the
// owner check the store performs.
func (r *Resolver) mapActorFor(ctx context.Context, entry service.MapEntry) (service.MapActor, error) {
	acct, err := requireViewer(ctx)
	if err != nil {
		return service.MapActor{}, err
	}
	actor := service.MapActor{AccountID: acct.ID}
	if entry.Status == engine.MapStatusDraft && entry.OwnerID != acct.ID {
		return actor, codedError(service.CodeNotFound, "map not found")
	}
	if r.Service.IsBuiltinMap(entry.Map.ID) || entry.OwnerID == "" {
		if err := r.checkAdminKey(ctx); err != nil {
			return actor, err
		}
		actor.Admin = true
	}
	return actor, nil
}

// codedError builds a gqlerror carrying extensions.code.
func codedError(code, msg string) *gqlerror.Error {
	return &gqlerror.Error{
		Message:    msg,
		Extensions: map[string]any{"code": code},
	}
}

// toGQLError maps service/engine errors to gqlerrors with extensions.code.
// Coded service errors keep their code; map validation failures become
// INVALID_ARGUMENT; anything else is logged and reported as INTERNAL.
func toGQLError(err error) error {
	if err == nil {
		return nil
	}
	var ge *gqlerror.Error
	if errors.As(err, &ge) {
		return ge
	}
	var se *service.Error
	if errors.As(err, &se) {
		return codedError(se.Code, se.Msg)
	}
	var ve *engine.ValidationError
	if errors.As(err, &ve) {
		return codedError(service.CodeInvalidArgument, ve.Error())
	}
	var pe *engine.ProgramError
	if errors.As(err, &pe) {
		return codedError(service.CodeInvalidProgram, pe.Error())
	}
	log.Printf("graph: internal error: %v", err)
	return codedError(CodeInternal, "internal error")
}

// isNotFound reports whether err is a coded NOT_FOUND service error.
func isNotFound(err error) bool {
	var se *service.Error
	return errors.As(err, &se) && se.Code == service.CodeNotFound
}
