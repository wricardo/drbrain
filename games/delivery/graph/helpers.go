package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/vektah/gqlparser/v2/gqlerror"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
)

// Error codes used by the city API. Session mutations keep their historical
// message-prefix codes; the city mutations are new and use extensions.code.
const (
	codeUnauthenticated = "UNAUTHENTICATED"
	codeForbidden       = "FORBIDDEN"
	codeNotFound        = "NOT_FOUND"
	codeInvalidArgument = "INVALID_ARGUMENT"
	codeLimitReached    = "LIMIT_REACHED"
	codeInternal        = "INTERNAL"
)

// codedErr builds a resolver error with a GraphQL extensions.code.
func codedErr(code, msg string) error {
	return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": code}}
}

// prefixedCodedErr keeps the historical "code: message" text while also
// exposing the machine-readable code through extensions.
func prefixedCodedErr(prefix, code, msg string) error {
	return codedErr(code, fmt.Sprintf("%s: %s", prefix, msg))
}

// cityCodedError maps the catalogue/store vocabulary to the city API codes.
func cityCodedError(err error) error {
	switch {
	case errors.Is(err, cityconfig.ErrNotFound):
		return codedErr(codeNotFound, err.Error())
	case errors.Is(err, cityconfig.ErrForbidden), errors.Is(err, cityconfig.ErrReadOnly):
		return codedErr(codeForbidden, err.Error())
	case errors.Is(err, cityconfig.ErrLimit):
		return codedErr(codeLimitReached, err.Error())
	case errors.Is(err, cityconfig.ErrExists),
		errors.Is(err, cityconfig.ErrInvalid),
		errors.Is(err, cityconfig.ErrDefaultCity):
		return codedErr(codeInvalidArgument, err.Error())
	default:
		return codedErr(codeInternal, err.Error())
	}
}

// Resolver helpers live here, NOT in schema.resolvers.go: gqlgen rewrites that
// file on every generate and moves unknown code to the end, so helpers kept
// there are fragile. Only resolver methods belong in the generated file.

// gqlErr builds a resolver error whose message begins with a machine-readable
// code, so a client can branch without an extensions scheme the schema does not
// declare.
func gqlErr(code, msg string) error {
	return fmt.Errorf("%s: %s", code, msg)
}

// errNotFound reports a missing entity or save.
func errNotFound(msg string) error { return gqlErr("not_found", msg) }

// errBadInput reports a malformed or out-of-range argument.
func errBadInput(msg string) error { return gqlErr("bad_request", msg) }

// errConflict reports a legal request in the wrong state: unaffordable, already
// done, not unlocked.
func errConflict(msg string) error { return gqlErr("conflict", msg) }

// errForbidden reports a state-changing request from a caller that cannot prove
// it created the session (wrong or missing owner token, another account).
func errForbidden(msg string) error { return gqlErr("forbidden", msg) }

// loadRaw resolves a session without applying city visibility. Session
// mutations use it so an owner-token holder can still play a draft-city session
// without being signed in; read paths use load below.
func (r *Resolver) loadRaw(id string) (*session.Session, error) {
	sess, err := r.Mgr.GetOrLoad(id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, errNotFound(err.Error())
		}
		return nil, err
	}
	return sess, nil
}

// load resolves a session for reading, mapping a missing save to not_found. A
// session on another player's DRAFT city is hidden exactly like a missing save.
func (r *Resolver) load(ctx context.Context, id string) (*session.Session, error) {
	sess, err := r.loadRaw(id)
	if err != nil {
		return nil, err
	}
	visible, err := r.sessionCityVisible(ctx, sess.CityID())
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, errNotFound("session not found")
	}
	return sess, nil
}

// catalogue returns the configured city catalog or an INTERNAL error.
func (r *Resolver) catalogue() (*cityconfig.Manager, error) {
	if r.Catalog == nil {
		return nil, codedErr(codeInternal, "city catalog is not configured")
	}
	return r.Catalog, nil
}

// parseDraftCity decodes raw city JSON and runs the structural gate. It is the
// authoring validator: a city that cannot yet be played may still be saved.
func parseDraftCity(raw string) (*engine.CityConfig, error) {
	cfg, err := engine.DecodeCityConfig([]byte(raw))
	if err != nil {
		return nil, codedErr(codeInvalidArgument, err.Error())
	}
	if err := engine.ValidateCityConfigStructural(cfg); err != nil {
		return nil, codedErr(codeInvalidArgument, err.Error())
	}
	return cfg, nil
}

// cityViewForEntry renders a catalogue row. The caller has already applied
// visibility/ownership rules.
func cityViewForEntry(entry cityconfig.Entry, viewerID string) (*model.CityView, error) {
	view, err := cityModelFrom(entry, viewerID)
	if err != nil {
		return nil, codedErr(codeInternal, err.Error())
	}
	return view, nil
}
