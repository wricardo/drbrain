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
	"os"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/wricardo/drbrain-motor-programming/game/engine"
	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/transport/websocket"
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
func checkAdminKey(ctx context.Context) error {
	required := os.Getenv("ADMIN_API_KEY")
	if required == "" {
		if os.Getenv("ALLOW_UNAUTHENTICATED_ADMIN") == "true" {
			return nil
		}
		return codedError(service.CodeForbidden, "admin operation disabled: set ADMIN_API_KEY and send X-Admin-Key")
	}
	r, _ := ctx.Value(HTTPRequestKey{}).(*http.Request)
	if r == nil {
		return codedError(service.CodeForbidden, "admin operation requires X-Admin-Key header")
	}
	// Compare fixed-length digests so the key length is not leaked.
	got := sha256.Sum256([]byte(r.Header.Get("X-Admin-Key")))
	want := sha256.Sum256([]byte(required))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return codedError(service.CodeForbidden, "forbidden: invalid or missing X-Admin-Key")
	}
	return nil
}

// checkMapAdmin gates a write to an existing map: player-created maps are open
// to everyone, shipped maps need the admin key.
func checkMapAdmin(ctx context.Context, builtin bool) error {
	if !builtin {
		return nil
	}
	return checkAdminKey(ctx)
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
