package model

// Admin is the target of Mutation.admin(key).
//
// The mutation validates the key before this object exists, so the value
// carries no state: holding one proves the caller passed the admin check. Both
// fields are resolver-driven (see games/motor/gqlgen.yml).
type Admin struct{}
