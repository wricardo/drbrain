package graph

import "testing"

// TestWaitUntilMutation covers the paused rejection, argument validation and a
// real wait on the running clock. It needs TEST_DATABASE_URL like the rest of
// the resolver suite.
func TestWaitUntilMutation(t *testing.T) {
	ts := newTestServer(t)

	var created struct {
		CreateSession struct {
			Session    struct{ ID string }
			OwnerToken string
		}
	}
	mustQuery(t, ts, `mutation { createSession(seed: 7) { session { id } ownerToken } }`, nil, &created)
	id, tok := created.CreateSession.Session.ID, created.CreateSession.OwnerToken

	// Sessions start paused: waiting for a future tick must fail, not hang.
	res := query(t, ts, `mutation { waitUntil(id: "`+id+`", tick: 1, ownerToken: "`+tok+`") { tick } }`, nil)
	if len(res.Errors) == 0 {
		t.Fatal("waitUntil succeeded on a paused session")
	}
	if code := cityErrCode(res); code != codeInvalidArgument {
		t.Fatalf("paused wait code = %q, want %s", code, codeInvalidArgument)
	}

	// Exactly one of tick/ticks.
	res = query(t, ts, `mutation { waitUntil(id: "`+id+`", tick: 5, ticks: 5, ownerToken: "`+tok+`") { tick } }`, nil)
	if code := cityErrCode(res); code != codeInvalidArgument {
		t.Fatalf("both arguments code = %q, want %s", code, codeInvalidArgument)
	}

	// A target at or behind the current tick is not a wait.
	var now struct {
		Session struct{ Tick int }
	}
	mustQuery(t, ts, `query { session(id: "`+id+`") { tick } }`, nil, &now)
	res = query(t, ts, `mutation { waitUntil(id: "`+id+`", tick: `+itoa(now.Session.Tick)+`, ownerToken: "`+tok+`") { tick } }`, nil)
	if code := cityErrCode(res); code != codeInvalidArgument {
		t.Fatalf("past target code = %q, want %s", code, codeInvalidArgument)
	}

	// On a running clock the mutation returns the snapshot at or past the target.
	mustQuery(t, ts, `mutation { setSpeed(id: "`+id+`", speed: 4, ownerToken: "`+tok+`") { speed } }`, nil, nil)
	start := now.Session.Tick
	var waited struct {
		WaitUntil struct {
			Tick   int
			Paused bool
		}
	}
	mustQuery(t, ts, `mutation { waitUntil(id: "`+id+`", ticks: 2, ownerToken: "`+tok+`") { tick paused } }`, nil, &waited)
	if waited.WaitUntil.Tick < start+2 {
		t.Fatalf("tick after waiting 2 = %d, want >= %d", waited.WaitUntil.Tick, start+2)
	}
	if waited.WaitUntil.Paused {
		t.Fatal("snapshot reports paused after a successful wait on a running clock")
	}
}
