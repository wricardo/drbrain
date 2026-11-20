package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter is a small in-memory fixed-window rate limiter keyed by client IP.
// It is meant for the few /auth routes, not for general traffic; state is lost
// on restart, which is fine for abuse control.
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweepAt time.Time
}

type bucket struct {
	count int
	reset time.Time
}

// maxBuckets bounds memory if many distinct addresses show up inside a window.
const maxBuckets = 10000

// NewLimiter allows limit requests per window per key. now may be nil.
func NewLimiter(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, window: window, now: now, buckets: make(map[string]*bucket)}
}

// Allow records a request from key and reports whether it is within the limit;
// retryAfter says how long until the window resets when it is not.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.After(l.sweepAt) || len(l.buckets) >= maxBuckets {
		for k, b := range l.buckets {
			if !now.Before(b.reset) {
				delete(l.buckets, k)
			}
		}
		l.sweepAt = now.Add(l.window)
	}
	b, found := l.buckets[key]
	if !found || !now.Before(b.reset) {
		if !found && len(l.buckets) >= maxBuckets {
			return false, l.window // table full of live buckets: refuse rather than grow
		}
		b = &bucket{reset: now.Add(l.window)}
		l.buckets[key] = b
	}
	if b.count >= l.limit {
		return false, b.reset.Sub(now)
	}
	b.count++
	return true, 0
}

// ClientIP is the address to rate-limit by. The peer address is used as is,
// except behind a reverse proxy on the same machine (the peer is loopback),
// where the last X-Forwarded-For entry (the one the proxy itself appended) is
// the client. Earlier entries are client-supplied and never trusted.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(last) != nil {
				return last
			}
		}
	}
	return host
}

// FailTracker counts failed attempts per key and locks a key out for a while
// once it has too many inside a window. Unlike Limiter it counts only failures,
// and Blocked does not count. Used to slow down password guessing: a locked key
// is refused before any password hashing happens.
type FailTracker struct {
	maxFails int
	window   time.Duration
	lock     time.Duration
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*failEntry
}

type failEntry struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

// NewFailTracker locks a key for lock after maxFails failures within window. now may be nil.
func NewFailTracker(maxFails int, window, lock time.Duration, now func() time.Time) *FailTracker {
	if now == nil {
		now = time.Now
	}
	return &FailTracker{maxFails: maxFails, window: window, lock: lock, now: now, entries: make(map[string]*failEntry)}
}

// Blocked reports whether key is locked out and for how long.
func (f *FailTracker) Blocked(key string) (bool, time.Duration) {
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.entries[key]; ok && now.Before(e.lockedUntil) {
		return true, e.lockedUntil.Sub(now)
	}
	return false, 0
}

// Fail records one failed attempt for key.
func (f *FailTracker) Fail(key string) {
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) >= maxBuckets {
		for k, e := range f.entries {
			if !now.Before(e.lockedUntil) && now.Sub(e.windowStart) >= f.window {
				delete(f.entries, k)
			}
		}
		for k := range f.entries { // still full: make room rather than refuse to track
			if len(f.entries) < maxBuckets {
				break
			}
			delete(f.entries, k)
		}
	}
	e, ok := f.entries[key]
	if !ok || now.Sub(e.windowStart) >= f.window {
		e = &failEntry{windowStart: now, lockedUntil: e0(e)}
		f.entries[key] = e
	}
	e.count++
	if e.count >= f.maxFails {
		e.lockedUntil = now.Add(f.lock)
		e.count = 0
		e.windowStart = now
	}
}

// e0 keeps an existing lock when a fresh window starts.
func e0(e *failEntry) time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.lockedUntil
}

// Clear forgets key's failures (after a successful sign-in).
func (f *FailTracker) Clear(key string) {
	f.mu.Lock()
	delete(f.entries, key)
	f.mu.Unlock()
}
