package service

import "github.com/wricardo/drbrain/platform/errs"

// Error codes surfaced as GraphQL extensions.code.
const (
	CodeNotFound        = errs.CodeNotFound
	CodeInvalidProgram  = errs.CodeInvalidProgram
	CodeSessionPlaying  = errs.CodeSessionPlaying
	CodeSessionTerminal = errs.CodeSessionTerminal
	CodeInvalidArgument = errs.CodeInvalidArgument
	CodeUnauthenticated = errs.CodeUnauthenticated
	CodeForbidden       = errs.CodeForbidden
	CodeLimit           = errs.CodeLimit
)

// Error is a coded service error.
type Error = errs.Error

// Errorf builds a coded error.
var Errorf = errs.Errorf
