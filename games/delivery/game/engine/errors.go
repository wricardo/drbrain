package engine

import "errors"

// Caller-facing lookup failures. They are sentinels so a transport can map a
// missing car or order to NOT_FOUND instead of guessing from the message.
var (
	ErrNoCar         = errors.New("no car")
	ErrNoOrder       = errors.New("no order")
	ErrNoDepot       = errors.New("no depot")
	ErrMoveQueueFull = errors.New("move queue full")
)
