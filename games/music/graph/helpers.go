package graph

import (
	"errors"
	"log"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/wricardo/drbrain/games/music/game/service"
)

// CodeInternal is the extensions.code for unexpected server failures.
const CodeInternal = "INTERNAL"

// codedError builds a gqlerror carrying extensions.code.
func codedError(code, msg string) *gqlerror.Error {
	return &gqlerror.Error{
		Message:    msg,
		Extensions: map[string]any{"code": code},
	}
}

// toGQLError maps service errors to gqlerrors with extensions.code. Coded
// service errors keep their code; anything else is logged and reported as
// INTERNAL.
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
	log.Printf("music graph: internal error: %v", err)
	return codedError(CodeInternal, "internal error")
}

// isNotFound reports whether err is a coded NOT_FOUND service error.
func isNotFound(err error) bool {
	var se *service.Error
	return errors.As(err, &se) && se.Code == service.CodeNotFound
}
