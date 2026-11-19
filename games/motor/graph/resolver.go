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
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/transport/websocket"
)

// CodeInternal is the extensions.code for unexpected server failures.
const CodeInternal = "INTERNAL"

// subscriptionBuffer is the output channel size of the subscription forwarder.
const subscriptionBuffer = 8

// Resolver is the gqlgen dependency injection root.
type Resolver struct {
	Service service.GameService
	Hub     *websocket.Hub
	// Providers lists the enabled sign-in providers (empty = sign-in is off).
	Providers []Provider
	// Passwords serves the username/password mutations; nil when sign-in is
	// off (the schema then answers FORBIDDEN).
	Passwords Passwords
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

// Passwords is the username/password sign-in of the auth package. The mutations
// call it, so the rules it enforces (same-origin, rate limits, lockouts) cannot
// be bypassed by a different transport.
type Passwords interface {
	PasswordSignIn(register bool, r *http.Request, handle, password string, setSession func(token string, expires time.Time)) (*service.Account, error)
}

// Provider is a sign-in provider as shown to clients.
type Provider struct{ ID, Name string }

// NewResolver builds the resolver root from the game service and the
// subscription hub.
func NewResolver(svc service.GameService, hub *websocket.Hub) *Resolver {
	return &Resolver{Service: svc, Hub: hub}
}

// HTTPRequestKey is the context key under which the *http.Request is stored
// by the HTTP middleware, so resolvers can read headers (X-Admin-Key).
type HTTPRequestKey struct{}

// ViewerKey is the context key under which the HTTP middleware stores the
// *Viewer of a request.
type ViewerKey struct{}

// SignInKey is the context key under which the HTTP middleware stores the
// sign-in hooks that need the response (setting the login cookie).
type SignInKey struct{}

// SignIn is what the password mutations need from the transport: it sets the
// login cookie on the response and reports whether that is possible at all.
// overHTTP is false for a websocket connection, which has no response to set a
// cookie on, so signing in there would hand out a login nobody could keep.
type SignIn struct {
	OverHTTP bool
	// SetSession writes the login cookie. Only call it when OverHTTP.
	SetSession func(token string, expires time.Time)
}

// Viewer is the signed-in player of a request. It is only set when the login
// cookie is valid and the request passed the same-origin check, so a cross-site
// page can never act as the player.
type Viewer struct {
	Account *service.Account
	// Token is the login cookie's value (for logout).
	Token string
	// ClearSession removes the login cookie from the response.
	ClearSession func()
}

// ViewerFrom returns the request's viewer, or nil.
func ViewerFrom(ctx context.Context) *Viewer {
	v, _ := ctx.Value(ViewerKey{}).(*Viewer)
	if v == nil || v.Account == nil {
		return nil
	}
	return v
}

// viewerID is the viewer's account id, "" for guests.
func viewerID(ctx context.Context) string {
	if v := ViewerFrom(ctx); v != nil {
		return v.Account.ID
	}
	return ""
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

// requireSignIn returns the request's sign-in hooks or a FORBIDDEN error when
// the cookie cannot be set (a subscription over websocket, or sign-in off).
func requireSignIn(ctx context.Context) (*SignIn, error) {
	s, _ := ctx.Value(SignInKey{}).(*SignIn)
	if s == nil || !s.OverHTTP {
		return nil, codedError(service.CodeForbidden, "sign-in needs a request with a response; use HTTP, not a subscription")
	}
	return s, nil
}

// requireViewer returns the signed-in account or an UNAUTHENTICATED error.
func requireViewer(ctx context.Context) (*service.Account, error) {
	if v := ViewerFrom(ctx); v != nil {
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

// checkMapAdmin gates a write to an existing map: player-created maps are open
// to everyone, shipped maps need the admin key.
func (r *Resolver) checkMapAdmin(ctx context.Context, builtin bool) error {
	if !builtin {
		return nil
	}
	return r.checkAdminKey(ctx)
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
