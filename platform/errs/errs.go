// Package errs defines the coded errors surfaced to GraphQL as
// extensions.code. It is shared by platform services (accounts, auth) and the
// games, so every layer reports the same codes.
package errs

import "fmt"

// Error codes surfaced as GraphQL extensions.code.
const (
	CodeNotFound        = "NOT_FOUND"
	CodeInvalidProgram  = "INVALID_PROGRAM"
	CodeSessionPlaying  = "SESSION_PLAYING"
	CodeSessionTerminal = "SESSION_TERMINAL"
	CodeInvalidArgument = "INVALID_ARGUMENT"
	CodeUnauthenticated = "UNAUTHENTICATED"
	CodeForbidden       = "FORBIDDEN"
	CodeLimit           = "LIMIT_REACHED"
	CodeInternal        = "INTERNAL"
)

// Error is a coded service error.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Errorf builds a coded error.
func Errorf(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}
