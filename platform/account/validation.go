package account

import (
	"errors"
	"regexp"
)

// Ids and provider names are validated before they reach the database.
var (
	idRE = regexp.MustCompile(`^[0-9a-f]{24}$`)
	// providerRE keeps provider names safe to embed in an index key.
	providerRE = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)
)

// ErrInvalidID is returned for an account id of the wrong shape.
var ErrInvalidID = errors.New("account: invalid id")
