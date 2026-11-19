//go:build tools

// Package tools pins code-generation binaries so `go mod tidy` keeps their
// dependencies in go.sum. Without this, gqlgen's own packages vanish from the
// module graph and `make generate` fails with missing go.sum entries.
package tools

import _ "github.com/99designs/gqlgen"
