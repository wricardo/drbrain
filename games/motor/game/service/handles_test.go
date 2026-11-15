package service_test

import (
	"strings"
	"testing"

	"github.com/wricardo/drbrain-motor-programming/game/service"
)

func TestValidateHandle(t *testing.T) {
	tests := []struct {
		name   string
		handle string
		ok     bool
	}{
		{"simple", "brave_otter", true},
		{"digits", "otter2024", true},
		{"min length", "abc", true},
		{"max length", strings.Repeat("a", 20), true},
		{"too short", "ab", false},
		{"too long", strings.Repeat("a", 21), false},
		{"upper case is not normalised here", "Brave", false},
		{"space", "brave otter", false},
		{"dash", "brave-otter", false},
		{"dot", "brave.otter", false},
		{"unicode", "bravéotter", false},
		{"empty", "", false},
		{"reserved", "admin", false},
		{"reserved route", "leaderboard", false},
		{"reserved me", "me_", true},
		{"profanity", "fuckface", false},
		{"leet profanity", "sh1tlord", false},
		{"separator evasion", "f_u_c_k_x", false},
		{"harmless lookalike", "classic", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := service.ValidateHandle(tc.handle)
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateHandle(%q) = %v, want ok=%v", tc.handle, err, tc.ok)
			}
			if err != nil {
				wantCode(t, err, service.CodeInvalidArgument)
			}
		})
	}
}

func TestNormalizeHandle(t *testing.T) {
	for in, want := range map[string]string{"  BraveOtter ": "braveotter", "@Fox_9": "fox_9", "x": "x", "": ""} {
		if got := service.NormalizeHandle(in); got != want {
			t.Errorf("NormalizeHandle(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every combination the generator can produce must be a valid handle, and a
// retry after a clash must differ.
func TestSuggestHandleAlwaysValid(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		attempt := i % 6
		h := service.SuggestHandle(attempt)
		if err := service.ValidateHandle(h); err != nil {
			t.Fatalf("SuggestHandle(%d) = %q: %v", attempt, h, err)
		}
		seen[h] = true
	}
	if len(seen) < 500 {
		t.Fatalf("only %d distinct handles in 5000 draws", len(seen))
	}
}

func TestSuggestHandleCombinationsPassBlocklist(t *testing.T) {
	// Exhaustive over the guest word lists (GuestName uses the same lists).
	for i := 0; i < 20000; i++ {
		if service.Blocked(service.GuestName(0)) {
			t.Fatal("a generated guest name is on the blocklist")
		}
	}
}

func TestCleanAccountDisplayName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "Brave Otter", "Brave Otter", false},
		{"trims and collapses", "  Brave \t Otter  ", "Brave Otter", false},
		{"control characters become spaces", "Brave‮Otter", "Brave Otter", false},
		{"empty falls back to handle", "   ", "", false},
		{"only invisible characters falls back", "​​", "", false},
		{"blocked", "Big Sh1t", "", true},
		{"too long", strings.Repeat("a", 41), "", true},
		{"max length ok", strings.Repeat("a", 40), strings.Repeat("a", 40), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := service.CleanAccountDisplayName(tc.in, 40)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
