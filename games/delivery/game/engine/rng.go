package engine

import "time"

// Deterministic randomness.
//
// Order spawning and jitter must be reproducible: a save is (seed, commands),
// and replaying it has to produce the same city. So instead of a stateful RNG
// stream — whose position depends on how many times it was called, and is
// therefore fragile when a code path is added — every random decision is a
// pure function of (seed, tick, a label).
//
// That makes replay exact even if the number of random draws per tick changes
// between versions, as long as the labels are stable.

// hashString is FNV-1a, used to fold labels into the seed.
func hashString(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// mix combines values with a splitmix-style finalizer so nearby inputs produce
// very different outputs.
func mix(v uint64) uint64 {
	v ^= v >> 30
	v *= 0xbf58476d1ce4e5b9
	v ^= v >> 27
	v *= 0x94d049bb133111eb
	v ^= v >> 31
	return v
}

// Roll returns a deterministic value in [0, n) for (seed, tick, label).
// It returns 0 when n <= 0 so callers need no special case.
func Roll(seed uint64, tick int64, label string, n int) int {
	if n <= 0 {
		return 0
	}
	h := mix(seed ^ mix(uint64(tick)) ^ mix(hashString(label)))
	return int(h % uint64(n))
}

// RollRange returns a deterministic value in [lo, hi] inclusive.
func RollRange(seed uint64, tick int64, label string, lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + Roll(seed, tick, label, hi-lo+1)
}

// Pick returns a deterministic element of choices, or "" when empty.
func Pick(seed uint64, tick int64, label string, choices []string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[Roll(seed, tick, label, len(choices))]
}

// RandomSeed returns a seed from a monotonic clock, for callers that want each
// session to differ. Tests and replays pass an explicit seed instead.
func RandomSeed() uint64 {
	return uint64(time.Now().UnixNano())
}
