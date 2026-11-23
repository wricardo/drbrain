package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	codeConflict        = "CONFLICT"
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
func errNotFound(msg string) error {
	return prefixedCodedErr("not_found", codeNotFound, msg)
}

// errBadInput reports a malformed or out-of-range argument.
func errBadInput(msg string) error {
	return prefixedCodedErr("bad_request", codeInvalidArgument, msg)
}

// errConflict reports a legal request in the wrong state: unaffordable, already
// done, not unlocked.
func errConflict(msg string) error {
	return prefixedCodedErr("conflict", codeConflict, msg)
}

// errForbidden reports a state-changing request from a caller that cannot prove
// it created the session (wrong or missing owner token, another account).
func errForbidden(msg string) error {
	return prefixedCodedErr("forbidden", codeForbidden, msg)
}

// errLimit reports a contract limit: a per-car queue or per-owner quota.
func errLimit(msg string) error {
	return prefixedCodedErr("limit_reached", codeLimitReached, msg)
}

// intOrZero dereferences an optional Int argument. A missing limit means the
// resolver applies its own default downstream.
func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// nonNilCashEntries and nonNilMonths keep the non-null list contract: a nil
// slice would serialize as null and fail the field type, while an empty window
// is a legal answer.
func nonNilCashEntries(entries []*model.CashEntryView) []*model.CashEntryView {
	if entries == nil {
		return []*model.CashEntryView{}
	}
	return entries
}

func nonNilMonths(months []*model.MonthView) []*model.MonthView {
	if months == nil {
		return []*model.MonthView{}
	}
	return months
}

// Session-list bounds. The default matches the other games' list default; the
// max caps one call, because every returned row costs a city-visibility lookup.
const (
	DefaultSessionListLimit = 50
	MaxSessionListLimit     = 500
)

// sessionSortFrom maps the GraphQL order to the session package's key.
func sessionSortFrom(s model.SessionSort) session.SessionSort {
	switch s {
	case model.SessionSortTick:
		return session.SortTick
	case model.SessionSortMoney:
		return session.SortMoney
	default:
		return session.SortCreatedAt
	}
}

// intPtrValue returns *p or fallback when p is nil.
func intPtrValue(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// DefaultEventFeedLimit bounds an events read when the caller names no limit.
const DefaultEventFeedLimit = 200

// Order-filter bounds. The default covers a long save's open jobs plus a useful
// finished tail; the max caps what one operation may return.
const (
	DefaultOrderFilterLimit = 2000
	MaxOrderFilterLimit     = 2000
)

// maxWaitTicks caps one waitUntil call, mirroring the tick mutation's cap so a
// single request cannot hold a connection open for an unbounded stretch.
const maxWaitTicks = 1000

// loadRaw resolves a session without applying city visibility. Session
// mutations use it so an owner-token holder can still play a draft-city session
// without being signed in; read paths use load below.
func (r *Resolver) loadRaw(id string) (*session.Session, error) {
	if !session.ValidID(id) {
		return nil, errBadInput(fmt.Sprintf("invalid session id %q", id))
	}
	sess, err := r.Mgr.GetOrLoad(id)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, errNotFound(err.Error())
		}
		if errors.Is(err, session.ErrCityNotFound) {
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

// engineMutationError maps an engine state error to the documented codes:
// a missing car/order/depot is NOT_FOUND, a malformed name is
// INVALID_ARGUMENT, and everything else is a legal request in the wrong state
// (unaffordable, already done, not unlocked) — CONFLICT.
func engineMutationError(err error) error {
	switch {
	case errors.Is(err, engine.ErrNoCar),
		errors.Is(err, engine.ErrNoOrder),
		errors.Is(err, engine.ErrNoDepot):
		return errNotFound(err.Error())
	case errors.Is(err, engine.ErrMoveQueueFull):
		return errLimit(err.Error())
	case strings.HasPrefix(err.Error(), "unknown car class"),
		strings.HasPrefix(err.Error(), "bad direction"):
		return errBadInput(err.Error())
	default:
		return errConflict(err.Error())
	}
}

// validateHistoryArgs rejects an analytics read the session layer would refuse
// with an uncoded error: too many ids, or an id that cannot name a session.
func validateHistoryArgs(ids []string) error {
	if len(ids) > session.MaxHistorySessions {
		return errBadInput(fmt.Sprintf("at most %d sessions per query", session.MaxHistorySessions))
	}
	for _, id := range ids {
		if !session.ValidID(id) {
			return errBadInput(fmt.Sprintf("invalid session id %q", id))
		}
	}
	return nil
}

// seriesRequest maps the shared analytics arguments to the session reader.
func (r *Resolver) seriesRequest(ctx context.Context, ids []string, cityID *string, fromTick, toTick, maxPoints *int) session.SeriesRequest {
	req := session.SeriesRequest{IDs: ids}
	if cityID != nil {
		req.CityID = *cityID
	}
	if fromTick != nil {
		v := int64(*fromTick)
		req.FromTick = &v
	}
	if toTick != nil {
		v := int64(*toTick)
		req.ToTick = &v
	}
	if maxPoints != nil {
		req.MaxPoints = *maxPoints
	}
	// Draft-city sessions inherit their city's privacy; analytics must not
	// become a side channel that lists or plots them.
	req.Visible = func(cityID string) bool {
		visible, err := r.sessionCityVisible(ctx, cityID)
		return err == nil && visible
	}
	return req
}
