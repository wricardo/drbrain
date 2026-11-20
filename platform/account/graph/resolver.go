// Package graph is the generated GraphQL surface of the global account
// namespace. Resolvers delegate to the platform account and auth services.
package graph

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/wricardo/drbrain/platform/account"
	"github.com/wricardo/drbrain/platform/account/graph/model"
	"github.com/wricardo/drbrain/platform/auth"
	"github.com/wricardo/drbrain/platform/errs"
)

// Resolver is the dependency injection root for the account namespace.
type Resolver struct {
	Service *account.Service
	Auth    *auth.Auth
}

// New returns the account namespace resolver.
func New(service *account.Service, authn *auth.Auth) *Resolver {
	return &Resolver{Service: service, Auth: authn}
}

func (r *Resolver) passwordSignIn(ctx context.Context, register bool, handle, password string) (*model.User, error) {
	if r.Auth == nil {
		return nil, errs.Errorf(errs.CodeForbidden, "sign-in is off")
	}
	signIn, req, err := requireSignIn(ctx)
	if err != nil {
		return nil, err
	}
	acct, err := r.Auth.PasswordSignIn(register, req, handle, password, signIn.SetSession)
	if err != nil {
		return nil, toGQLError(err)
	}
	return r.toUser(acct), nil
}

func (r *Resolver) toUser(a *account.Account) *model.User {
	if a == nil {
		return nil
	}
	out := &model.User{
		Handle: a.Handle, DisplayName: a.DisplayName, ProfilePublic: a.ProfilePublic,
		CreatedAt: a.CreatedAt, HasPassword: a.HasPassword,
	}
	if r.Service != nil {
		if next := r.Service.HandleChangeableAt(a); !next.IsZero() {
			out.HandleChangeableAt = &next
		}
	}
	return out
}

func (r *Resolver) toProfile(p *account.Profile) *model.Profile {
	return &model.Profile{
		Handle: p.Account.Handle, DisplayName: p.Account.DisplayName,
		CreatedAt: p.Account.CreatedAt, IsMine: p.IsMine,
	}
}

func viewerID(ctx context.Context) string {
	if v := auth.ViewerFrom(ctx); v != nil {
		return v.Account.ID
	}
	return ""
}

func requireViewer(ctx context.Context) (*auth.Viewer, error) {
	v := auth.ViewerFrom(ctx)
	if v == nil {
		return nil, errs.Errorf(errs.CodeUnauthenticated, "sign in first")
	}
	return v, nil
}

func requireSignIn(ctx context.Context) (*auth.SignIn, *http.Request, error) {
	s := auth.SignInFrom(ctx)
	if s == nil || !s.OverHTTP {
		return nil, nil, errs.Errorf(errs.CodeForbidden, "sign-in needs an HTTP request")
	}
	req := auth.RequestFrom(ctx)
	if req == nil {
		return nil, nil, errs.Errorf(errs.CodeForbidden, "sign-in needs an HTTP request")
	}
	return s, req, nil
}

func toGQLError(err error) error {
	if err == nil {
		return nil
	}
	var ge *gqlerror.Error
	if errors.As(err, &ge) {
		return ge
	}
	var se *errs.Error
	if errors.As(err, &se) {
		return &gqlerror.Error{
			Message:    se.Msg,
			Extensions: map[string]any{"code": se.Code},
		}
	}
	log.Printf("account graph: internal error: %v", err)
	return &gqlerror.Error{
		Message:    "internal error",
		Extensions: map[string]any{"code": errs.CodeInternal},
	}
}

func isNotFound(err error) bool {
	var se *errs.Error
	return errors.As(err, &se) && se.Code == errs.CodeNotFound
}
