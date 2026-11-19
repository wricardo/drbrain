package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/account"
	"github.com/wricardo/drbrain-motor-programming/game/service"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name, pw, handle string
		ok               bool
	}{
		{"fine", "tangerine-ladder-42", "otter", true},
		{"spaces and symbols are fine", "a b c d e f g h", "otter", true},
		{"unicode counts characters", "ñandúñandú", "otter", true},
		{"too short", "abc1234", "otter", false},
		{"too short in characters", "ñññññññ", "otter", false},
		{"empty", "", "otter", false},
		{"too long", strings.Repeat("a", 129), "otter", false},
		{"max length", strings.Repeat("a", 128), "otter", true},
		{"common", "Password123", "otter", false},
		{"common 2", "12345678", "otter", false},
		{"same as username", "otterotter", "otterotter", false},
		{"same as username, other case", "OtterOtter", "otterotter", false},
		{"invalid utf8", "abcdefgh\xff", "otter", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := service.ValidatePassword(tc.pw, tc.handle)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidatePassword = %v, want ok=%v", err, tc.ok)
			}
			if err != nil {
				wantCode(t, err, service.CodeInvalidArgument)
			}
		})
	}
}

func newPwEnv(t *testing.T) *acctEnv {
	t.Helper()
	return newAcctEnv(t, service.Options{PasswordSignIn: true})
}

func TestPasswordSignInIsOffByDefault(t *testing.T) {
	e := newAcctEnv(t, service.Options{}) // accounts on, password off
	if _, err := e.svc.RegisterPassword("otter", "tangerine-ladder-42"); err == nil {
		t.Fatal("registration worked with AUTH_PASSWORD off")
	} else {
		wantCode(t, err, service.CodeForbidden)
	}
	if _, err := e.svc.VerifyPassword("otter", "x"); err == nil {
		t.Fatal("login worked with AUTH_PASSWORD off")
	}
	if err := e.svc.ChangePassword("0123456789abcdef01234567", "a", "b", ""); err == nil {
		t.Fatal("change worked with AUTH_PASSWORD off")
	}
}

func TestRegisterAndVerifyPassword(t *testing.T) {
	e := newPwEnv(t)
	a, err := e.svc.RegisterPassword("  Speedy_Fox ", "tangerine-ladder-42")
	if err != nil {
		t.Fatal(err)
	}
	if a.Handle != "speedy_fox" || a.DisplayName != "speedy_fox" || !a.ProfilePublic || !a.HasPassword || a.PasswordHash != "" {
		t.Fatalf("account = %+v", a)
	}
	if a.Provider != service.PasswordProvider {
		t.Fatalf("provider = %q", a.Provider)
	}

	for _, user := range []string{"speedy_fox", "SPEEDY_FOX", " @speedy_fox "} {
		got, err := e.svc.VerifyPassword(user, "tangerine-ladder-42")
		if err != nil || got.ID != a.ID || got.PasswordHash != "" || !got.HasPassword {
			t.Fatalf("VerifyPassword(%q) = %+v, %v", user, got, err)
		}
	}

	// A wrong password and an unknown username are indistinguishable.
	_, errWrong := e.svc.VerifyPassword("speedy_fox", "tangerine-ladder-43")
	_, errUnknown := e.svc.VerifyPassword("nobody_here", "tangerine-ladder-42")
	if !errors.Is(errWrong, service.ErrBadCredentials) || !errors.Is(errUnknown, service.ErrBadCredentials) {
		t.Fatalf("errors = %v / %v", errWrong, errUnknown)
	}
	if errWrong.Error() != errUnknown.Error() {
		t.Fatal("the two failures read differently")
	}
	wantCode(t, errWrong, service.CodeUnauthenticated)
	if _, err := e.svc.VerifyPassword("speedy_fox", strings.Repeat("a", 5000)); !errors.Is(err, service.ErrBadCredentials) {
		t.Fatalf("oversized password = %v", err)
	}
}

func TestRegisterPasswordRules(t *testing.T) {
	e := newPwEnv(t)
	if _, err := e.svc.RegisterPassword("speedy_fox", "tangerine-ladder-42"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, handle, pw string
	}{
		{"taken", "speedy_fox", "another-good-pass-1"},
		{"taken, other case", "SPEEDY_FOX", "another-good-pass-1"},
		{"short handle", "ab", "another-good-pass-1"},
		{"bad characters", "has space", "another-good-pass-1"},
		{"reserved", "admin", "another-good-pass-1"},
		{"blocked", "fuck_you", "another-good-pass-1"},
		{"weak password", "fresh_name", "password123"},
		{"short password", "fresh_name", "short"},
		{"password is the username", "fresh_name", "fresh_name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.RegisterPassword(tc.handle, tc.pw)
			if err == nil {
				t.Fatal("accepted")
			}
			wantCode(t, err, service.CodeInvalidArgument)
		})
	}
	if e.store.Count() != 1 {
		t.Fatalf("%d accounts, want 1", e.store.Count())
	}
}

func TestRegisterPasswordRespectsAccountCap(t *testing.T) {
	e := newAcctEnv(t, service.Options{PasswordSignIn: true, MaxAccounts: 1})
	if _, err := e.svc.RegisterPassword("first_one", "tangerine-ladder-42"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RegisterPassword("second_one", "tangerine-ladder-42"); err == nil {
		t.Fatal("over the cap")
	} else {
		wantCode(t, err, service.CodeLimit)
	}
}

// The password is never stored, the hash never leaves the service, and both
// survive a restart.
func TestPasswordHashAtRestAndNeverExposed(t *testing.T) {
	dir := t.TempDir()
	store, err := account.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, envOpts{svcOpts: service.Options{Accounts: store, PasswordSignIn: true}}, wideCfg)
	a, err := e.svc.RegisterPassword("speedy_fox", "tangerine-ladder-42")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _ := e.svc.StartLogin(a.ID)

	raw, _ := os.ReadFile(filepath.Join(dir, "accounts", a.ID+".json"))
	if strings.Contains(string(raw), "tangerine") || !strings.Contains(string(raw), `"password_hash":"$argon2id$`) {
		t.Fatalf("account file = %s", raw)
	}
	// Every account the service hands out has no hash but says it has a password.
	if got, _ := e.svc.GetAccount(a.ID); got.PasswordHash != "" || !got.HasPassword {
		t.Fatalf("GetAccount = %+v", got)
	}
	if got, ok := e.svc.Authenticate(tok); !ok || got.PasswordHash != "" || !got.HasPassword {
		t.Fatalf("Authenticate = %+v", got)
	}
	up, err := e.svc.UpdateAccount(a.ID, service.AccountUpdate{DisplayName: ptr("Speedy")})
	if err != nil || up.PasswordHash != "" || !up.HasPassword {
		t.Fatalf("UpdateAccount = %+v, %v", up, err)
	}
	if p, err := e.svc.GetProfile("speedy_fox", ""); err != nil || p.Account.PasswordHash != "" {
		t.Fatalf("GetProfile = %+v, %v", p, err)
	}
	// Changing the display name or handle must not wipe the password.
	if _, err := e.svc.VerifyPassword("speedy_fox", "tangerine-ladder-42"); err != nil {
		t.Fatalf("login after UpdateAccount: %v", err)
	}

	// After a restart the password still works.
	store2, err := account.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	e2 := newEnv(t, envOpts{svcOpts: service.Options{Accounts: store2, PasswordSignIn: true}}, wideCfg)
	if _, err := e2.svc.VerifyPassword("speedy_fox", "tangerine-ladder-42"); err != nil {
		t.Fatalf("login after restart: %v", err)
	}
}

func TestOAuthAccountsCannotUsePasswords(t *testing.T) {
	e := newPwEnv(t)
	g := e.signIn(t, "google-user")
	if _, err := e.svc.VerifyPassword(g.Handle, ""); !errors.Is(err, service.ErrBadCredentials) {
		t.Fatalf("empty password on an OAuth account = %v", err)
	}
	if got := mustGet(t, e, g.ID); got.HasPassword {
		t.Fatal("OAuth account claims to have a password")
	}
	if err := e.svc.ChangePassword(g.ID, "x", "tangerine-ladder-42", ""); err == nil {
		t.Fatal("set a password on an OAuth account")
	} else {
		wantCode(t, err, service.CodeInvalidArgument)
	}
}

func TestChangePassword(t *testing.T) {
	e := newPwEnv(t)
	a, _ := e.svc.RegisterPassword("speedy_fox", "tangerine-ladder-42")
	mine, _, _ := e.svc.StartLogin(a.ID)
	other, _, _ := e.svc.StartLogin(a.ID)
	third, _, _ := e.svc.StartLogin(a.ID)

	tests := []struct {
		name, current, next string
	}{
		{"wrong current", "not-my-password", "brand-new-pass-77"},
		{"weak new", "tangerine-ladder-42", "password123"},
		{"short new", "tangerine-ladder-42", "short"},
		{"new equals the username", "tangerine-ladder-42", "speedy_fox"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := e.svc.ChangePassword(a.ID, tc.current, tc.next, mine); err == nil {
				t.Fatal("accepted")
			} else {
				wantCode(t, err, service.CodeInvalidArgument)
			}
			if _, err := e.svc.VerifyPassword("speedy_fox", "tangerine-ladder-42"); err != nil {
				t.Fatal("a refused change altered the password")
			}
			if _, ok := e.svc.Authenticate(other); !ok {
				t.Fatal("a refused change signed other browsers out")
			}
		})
	}

	if err := e.svc.ChangePassword(a.ID, "tangerine-ladder-42", "brand-new-pass-77", mine); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.VerifyPassword("speedy_fox", "tangerine-ladder-42"); !errors.Is(err, service.ErrBadCredentials) {
		t.Fatal("old password still works")
	}
	if _, err := e.svc.VerifyPassword("speedy_fox", "brand-new-pass-77"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if _, ok := e.svc.Authenticate(mine); !ok {
		t.Fatal("the caller's own login was ended")
	}
	for _, tok := range []string{other, third} {
		if _, ok := e.svc.Authenticate(tok); ok {
			t.Fatal("another browser stayed signed in after the password change")
		}
	}
}

func TestDeletedPasswordAccountCannotLogIn(t *testing.T) {
	e := newPwEnv(t)
	a, _ := e.svc.RegisterPassword("speedy_fox", "tangerine-ladder-42")
	if err := e.svc.DeleteAccount(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.VerifyPassword("speedy_fox", "tangerine-ladder-42"); !errors.Is(err, service.ErrBadCredentials) {
		t.Fatalf("login after delete = %v", err)
	}
	// The username is free again.
	if _, err := e.svc.RegisterPassword("speedy_fox", "another-good-pass-1"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
}
