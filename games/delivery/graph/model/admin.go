package model

// Admin is the target of Mutation.deliveryAdmin(key).
//
// The mutation validates the key before this object exists, so the value
// carries no state: holding one proves the caller passed the admin check.
// deleteSession is a resolver-driven field (see games/delivery/gqlgen.yml),
// which is where the resolver's session manager is reachable.
type Admin struct{}
