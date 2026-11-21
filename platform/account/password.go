package account

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/wricardo/drbrain/platform/errs"
)

// Password rules. There is deliberately no email and no recovery: a forgotten
// password means the account cannot be recovered (the player can use another
// sign-in or start again), and the UI says so before sign-up.
const (
	MinPasswordLen = 8
	// MaxPasswordBytes bounds the work and memory one request can ask for.
	MaxPasswordBytes = 128

	// PasswordProvider is the Account.Provider of username/password accounts.
	PasswordProvider = "password"

	// argon2id parameters (OWASP's minimum recommendation: 19 MiB, 2 passes,
	// 1 lane). They are stored in each hash, so they can be raised later without
	// invalidating existing passwords.
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16

	// Hashes read back from disk are bounded so a hand-edited file cannot ask
	// for an absurd amount of memory.
	maxArgonMemory = 256 * 1024
	maxArgonTime   = 10

	// maxConcurrentHashes caps simultaneous argon2 computations (each uses ~19 MiB).
	maxConcurrentHashes = 4
)

// ErrBadCredentials is the single answer for a wrong password and an unknown
// username, so a login attempt cannot be used to find out who has an account.
var ErrBadCredentials = errs.Errorf(errs.CodeUnauthenticated, "wrong username or password")

var hashSlots = make(chan struct{}, maxConcurrentHashes)

// commonPasswords is a small embedded deny list of the most guessed passwords
// (compared lower-case). It is a floor, not a strength meter.
var commonPasswords = map[string]bool{
	"password": true, "password1": true, "password12": true, "password123": true, "passw0rd": true,
	"12345678": true, "123456789": true, "1234567890": true, "11111111": true, "00000000": true,
	"qwertyui": true, "qwerty123": true, "qwertyuiop": true, "asdfghjk": true, "abc12345": true,
	"abcd1234": true, "iloveyou": true, "letmein1": true, "welcome1": true, "welcome123": true,
	"monkey123": true, "dragon123": true, "football": true, "baseball": true, "superman": true,
	"trustno1": true, "sunshine": true, "princess": true, "admin123": true, "changeme": true,
	"minecraft": true, "roblox123": true, "fortnite": true, "pokemon1": true,
}

// ValidatePassword checks a new password against the rules. handle is the
// username it will go with ("" to skip that comparison).
func ValidatePassword(pw, handle string) error {
	switch {
	case !utf8.ValidString(pw):
		return errs.Errorf(errs.CodeInvalidArgument, "the password must be valid text")
	case utf8.RuneCountInString(pw) < MinPasswordLen:
		return errs.Errorf(errs.CodeInvalidArgument, "use at least %d characters", MinPasswordLen)
	case len(pw) > MaxPasswordBytes:
		return errs.Errorf(errs.CodeInvalidArgument, "the password is too long (at most %d bytes)", MaxPasswordBytes)
	case commonPasswords[strings.ToLower(pw)]:
		return errs.Errorf(errs.CodeInvalidArgument, "that password is too common; pick something harder to guess")
	case handle != "" && strings.EqualFold(pw, handle):
		return errs.Errorf(errs.CodeInvalidArgument, "the password cannot be the same as your username")
	}
	return nil
}

// hashPassword returns a PHC-format argon2id hash with a fresh random salt.
func hashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hashSlots <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	<-hashSlots
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword compares pw with a stored hash in constant time. Malformed or
// out-of-bounds hashes never match.
func verifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	if mem == 0 || mem > maxArgonMemory || t == 0 || t > maxArgonTime || p == 0 || p > 8 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err := errors.Join(err1, err2); err != nil || len(salt) < 8 || len(want) < 16 || len(want) > 64 {
		return false
	}
	hashSlots <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, mem, p, uint32(len(want)))
	<-hashSlots
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the username is unknown, so a miss costs
// the same as a wrong password and response time does not reveal which
// usernames exist.
var dummyHash = func() string {
	h, err := hashPassword("dummy password for timing equalisation")
	if err != nil {
		panic(err)
	}
	return h
}()

// RegisterPassword creates a username/password account. The username is the
// public handle (same rules and blocklist). The password is stored only as an
// argon2id hash.
func (s *Service) RegisterPassword(handle, password string) (*Account, error) {
	st := s.store
	if st == nil || !s.opts.PasswordSignIn {
		return nil, errAccountsOff()
	}
	handle = NormalizeHandle(handle)
	if err := ValidateHandle(handle); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password, handle); err != nil {
		return nil, err
	}
	if st.Count() >= s.maxAccounts() {
		return nil, errs.Errorf(errs.CodeLimit, "account limit reached")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	id, err := newAccountID()
	if err != nil {
		return nil, err
	}
	a := Account{
		ID: id, Provider: PasswordProvider, ProviderUserID: id, Handle: handle, DisplayName: handle,
		ProfilePublic: true, CreatedAt: s.now().UTC(), PasswordHash: hash,
	}
	if err := st.Create(a); err != nil {
		if errors.Is(err, ErrHandleTaken) {
			return nil, errs.Errorf(errs.CodeInvalidArgument, "that username is already taken")
		}
		return nil, err
	}
	a = publicAccount(a)
	return &a, nil
}

// VerifyPassword checks a username and password. Every failure is
// ErrBadCredentials; a miss still does one hash so timing is similar.
func (s *Service) VerifyPassword(handle, password string) (*Account, error) {
	st := s.store
	if st == nil || !s.opts.PasswordSignIn {
		return nil, errAccountsOff()
	}
	if len(password) > MaxPasswordBytes {
		return nil, ErrBadCredentials
	}
	a, ok := st.ByHandle(NormalizeHandle(handle))
	if !ok || a.Provider != PasswordProvider || a.PasswordHash == "" {
		verifyPassword(password, dummyHash)
		return nil, ErrBadCredentials
	}
	if !verifyPassword(password, a.PasswordHash) {
		return nil, ErrBadCredentials
	}
	a = publicAccount(a)
	return &a, nil
}

// ChangePassword sets a new password for a password account after checking the
// current one. Access tokens already issued keep working until they expire,
// because they are stateless.
func (s *Service) ChangePassword(accountID, current, next string) error {
	st := s.store
	if st == nil || !s.opts.PasswordSignIn {
		return errAccountsOff()
	}
	a, ok := st.Get(accountID)
	if !ok {
		return errs.Errorf(errs.CodeNotFound, "account not found")
	}
	if a.Provider != PasswordProvider || a.PasswordHash == "" {
		return errs.Errorf(errs.CodeInvalidArgument, "this account signs in with %s and has no password", a.Provider)
	}
	if len(current) > MaxPasswordBytes || !verifyPassword(current, a.PasswordHash) {
		return errs.Errorf(errs.CodeInvalidArgument, "the current password is wrong")
	}
	if err := ValidatePassword(next, a.Handle); err != nil {
		return err
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	if _, err := st.Update(accountID, func(a *Account) error { a.PasswordHash = hash; return nil }); err != nil {
		return err
	}
	return nil
}
