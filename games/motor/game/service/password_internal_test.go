package service

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash = %q", h)
	}
	if strings.Contains(h, "correct horse") {
		t.Fatal("the password is in its own hash")
	}
	h2, _ := hashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("two hashes of one password are identical: the salt is not random")
	}
	if !verifyPassword("correct horse battery", h) || !verifyPassword("correct horse battery", h2) {
		t.Fatal("right password rejected")
	}
	for _, wrong := range []string{"", "correct horse batter", "Correct horse battery", "correct horse battery "} {
		if verifyPassword(wrong, h) {
			t.Errorf("%q accepted", wrong)
		}
	}
}

// A hash read from a hand-edited file must never crash or make the server
// allocate absurd amounts of memory.
func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	good, _ := hashPassword("pw-for-the-test")
	tests := []struct{ name, hash string }{
		{"empty", ""},
		{"not phc", "plaintext"},
		{"wrong algorithm", strings.Replace(good, "argon2id", "argon2i", 1)},
		{"wrong version", strings.Replace(good, "v=19", "v=16", 1)},
		{"huge memory", strings.Replace(good, "m=19456", "m=4194304", 1)},
		{"zero memory", strings.Replace(good, "m=19456", "m=0", 1)},
		{"huge time", strings.Replace(good, "t=2", "t=1000", 1)},
		{"zero threads", strings.Replace(good, "p=1", "p=0", 1)},
		{"short salt", "$argon2id$v=19$m=19456,t=2,p=1$AAAA$" + strings.Split(good, "$")[5]},
		{"bad base64", "$argon2id$v=19$m=19456,t=2,p=1$!!!$!!!"},
		{"missing fields", "$argon2id$v=19$m=19456,t=2,p=1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if verifyPassword("pw-for-the-test", tc.hash) {
				t.Fatalf("accepted %q", tc.hash)
			}
		})
	}
}
