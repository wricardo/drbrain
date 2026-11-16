package graph

import (
	"context"
	"net/http"

	"github.com/wricardo/drbrain-motor-programming/game/service"
	"github.com/wricardo/drbrain-motor-programming/graph/model"
)

// passwordSignIn is the shared body of the registerWithPassword and
// logInWithPassword mutations (register selects which). It lives here, outside
// schema.resolvers.go, because gqlgen keeps only the resolvers the schema
// declares: a helper in that file would be moved aside on the next generate.
//
// The rules it enforces live behind the auth package's call, not here: that
// refuses a cross-site request, limits attempts per address and locks out
// failing (address, username) pairs and usernames, so this resolver cannot
// weaken them by accident. It only supplies what the mutation has and an HTTP
// route had as a matter of course: the request (for the same-origin check, the
// client address and the cookie the browser already holds) and a way to set the
// login cookie on the response.
func (r *mutationResolver) passwordSignIn(ctx context.Context, register bool, handle, password string) (*model.Account, error) {
	if r.Passwords == nil {
		return nil, codedError(service.CodeForbidden, "sign-in is off")
	}
	signIn, err := requireSignIn(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := ctx.Value(HTTPRequestKey{}).(*http.Request)
	if req == nil {
		return nil, codedError(service.CodeForbidden, "sign-in needs an HTTP request")
	}
	acct, err := r.Passwords.PasswordSignIn(register, req, handle, password, signIn.SetSession)
	if err != nil {
		return nil, toGQLError(err)
	}
	return r.toAccount(acct), nil
}
