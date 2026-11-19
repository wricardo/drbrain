package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestBasePathScopesAuthState pins the /motor deployment: cookies, the flow
// cookie, OAuth callbacks and redirect rejection all carry the prefix, while
// route registration stays relative.
func TestBasePathScopesAuthState(t *testing.T) {
	a, err := New(Config{
		Accounts:  newFakeAccounts(),
		Password:  true,
		PublicURL: "https://example.test",
		BasePath:  "/motor",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "https://example.test/motor/graphql", nil)

	rec := httptest.NewRecorder()
	a.SetSession(rec, req, "token", time.Now().Add(time.Hour))
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookie || cookies[0].Path != "/motor" {
		t.Fatalf("session cookie = %+v, want %s Path=/motor", cookies, SessionCookie)
	}

	rec = httptest.NewRecorder()
	a.setFlow(rec, req, "binding")
	flow := rec.Result().Cookies()
	if len(flow) != 1 || flow[0].Name != flowCookie || flow[0].Path != "/motor/auth" {
		t.Fatalf("flow cookie = %+v, want %s Path=/motor/auth", flow, flowCookie)
	}

	if got := a.callbackURL("google"); got != "https://example.test/motor/auth/google/callback" {
		t.Errorf("callbackURL = %q", got)
	}
	if got := a.safeNext("/motor/auth/google/callback?x=1"); got != "/" {
		t.Errorf("safeNext(/motor/auth/...) = %q, want /", got)
	}
	if got := a.safeNext("/motor/play/abc"); got != "/motor/play/abc" {
		t.Errorf("safeNext(/motor/play/abc) = %q", got)
	}
}
