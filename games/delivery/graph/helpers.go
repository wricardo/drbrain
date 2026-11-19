package graph

import (
	"errors"
	"fmt"

	"github.com/wricardo/drbrain/games/delivery/game/session"
)

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

// load resolves a session, mapping a missing save to not_found.
func (r *Resolver) load(id string) (*session.Session, error) {
	sess, err := r.Mgr.GetOrLoad(id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, errNotFound(err.Error())
		}
		return nil, err
	}
	return sess, nil
}
