//go:generate go run github.com/wricardo/drbrain/cmd/split-schema
//go:generate go run github.com/99designs/gqlgen generate

// Package account owns global player accounts; the graph subpackage is the
// generated GraphQL surface for the account namespace.
package account
