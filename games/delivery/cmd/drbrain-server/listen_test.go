package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBindFreePort Succeeds pins the ordinary path: nothing is holding the
// address, so bind must behave exactly like net.Listen and hand back a
// listener the caller can serve on.
func TestBindFreePortSucceeds(t *testing.T) {
	ln, err := bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind on a free port: %v", err)
	}
	defer ln.Close()

	if ln.Addr().(*net.TCPAddr).Port == 0 {
		t.Fatal("listener has port 0; the kernel did not assign one")
	}
}

// TestBindOccupiedByDrbrainServerNamesIt is the case that motivated the guard:
// a previous build is still answering, so the new schema never reaches the
// browser. The error has to say that, because "address already in use" alone
// invites the operator to rebuild and hit the identical error.
func TestBindOccupiedByDrbrainServerNamesIt(t *testing.T) {
	occupied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"health":{"status":"ok"}}}`))
	}))
	defer occupied.Close()

	_, err := bind(occupied.Listener.Addr().String())
	if err == nil {
		t.Fatal("bind over a running server returned nil error")
	}

	msg := err.Error()
	for _, want := range []string{
		"already in use",
		"running DrBrain server",
		`health "ok"`,
		"Cannot query field",
		"lsof",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q; got:\n%s", want, msg)
		}
	}
}

// TestBindOccupiedByOtherProcessDoesNotClaimDrbrain guards the diagnosis from
// being a guess. A port held by something unrelated (a dev server, another
// checkout's UI) is a different problem with a different fix, so the message
// must report the conflict without asserting what is on the port.
func TestBindOccupiedByOtherProcessDoesNotClaimDrbrain(t *testing.T) {
	occupied := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer occupied.Close()

	_, err := bind(occupied.Listener.Addr().String())
	if err == nil {
		t.Fatal("bind over an occupied port returned nil error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "already in use") {
		t.Errorf("error should still report the conflict; got:\n%s", msg)
	}
	if strings.Contains(msg, "DrBrain server") {
		t.Errorf("error claims a DrBrain server it could not verify; got:\n%s", msg)
	}
}

// TestBindReportsRealErrorsUnchanged makes sure the guard only speaks for port
// conflicts. A bad address must surface the reason it failed, not a misleading
// message about another process.
func TestBindReportsRealErrorsUnchanged(t *testing.T) {
	_, err := bind("not-an-address")
	if err == nil {
		t.Fatal("bind accepted a malformed address")
	}
	if strings.Contains(err.Error(), "already in use") {
		t.Errorf("malformed address misreported as a conflict: %v", err)
	}
}

func TestIsAddrInUse(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", net.ErrClosed, false},
		{"string fallback", errString("listen tcp 127.0.0.1:8080: bind: address already in use"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAddrInUse(tc.err); got != tc.want {
				t.Errorf("isAddrInUse(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestDialableProbesLoopbackForWildcardBind covers `-addr :8080` and
// `-addr 0.0.0.0:8080`, which bind every interface. Neither is a destination a
// client can dial, so the probe must fall back to loopback or the occupant
// goes unidentified.
func TestDialableProbesLoopbackForWildcardBind(t *testing.T) {
	cases := map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:8080":      "127.0.0.1:8080",
		"localhost:8080": "localhost:8080",
		"127.0.0.1:9000": "127.0.0.1:9000",
	}
	for in, want := range cases {
		if got := dialable(in); got != want {
			t.Errorf("dialable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStopHintNamesPort(t *testing.T) {
	got := stopHint("localhost:8081")
	if !strings.Contains(got, "lsof -nP -iTCP:8081") {
		t.Errorf("hint does not name the port: %q", got)
	}
}

// errString lets the fallback path be tested without depending on how a
// platform error happens to unwrap.
type errString string

func (e errString) Error() string { return string(e) }
