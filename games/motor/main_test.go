package main

import "testing"

// TestEnvBoolAcceptsTheSameSpellingsAsAuthFlags pins the contract the auth
// package already used: case and surrounding spaces must not change the answer.
// Before this, GRAPHQL_INTROSPECTION=TRUE read as false, which silently turned
// introspection off for exactly the operator who had just switched it on.
func TestEnvBoolAcceptsTheSameSpellingsAsAuthFlags(t *testing.T) {
	tests := []struct {
		name string
		val  string
		def  bool
		want bool
	}{
		{"unset falls back to the default (true)", "", true, true},
		{"unset falls back to the default (false)", "", false, false},
		{"lowercase true", "true", false, true},
		{"upper true", "TRUE", false, true},
		{"mixed true", "True", false, true},
		{"one", "1", false, true},
		{"yes", "yes", false, true},
		{"upper yes", "YES", false, true},
		{"on", "on", false, true},
		{"padded", "  true  ", false, true},
		{"false is false even when the default is true", "false", true, false},
		{"upper false", "FALSE", true, false},
		{"zero", "0", true, false},
		{"no", "no", true, false},
		{"unrecognised is false, not the default", "maybe", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DBRAIN_TEST_BOOL", tc.val)
			if got := envBool("DBRAIN_TEST_BOOL", tc.def); got != tc.want {
				t.Fatalf("envBool(%q, def=%v) = %v, want %v", tc.val, tc.def, got, tc.want)
			}
		})
	}
}

// TestEnvIntRejectsNonPositive keeps the companion helper's documented
// behaviour honest: a cap of 0 or a negative would disable the limit, so an
// invalid value must fall back to the default rather than through.
func TestEnvIntRejectsNonPositive(t *testing.T) {
	for _, val := range []string{"", "0", "-5", "abc"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("DBRAIN_TEST_INT", val)
			if got := envInt("DBRAIN_TEST_INT", 42); got != 42 {
				t.Fatalf("envInt(%q) = %d, want the default 42", val, got)
			}
		})
	}
	t.Setenv("DBRAIN_TEST_INT", "7")
	if got := envInt("DBRAIN_TEST_INT", 42); got != 7 {
		t.Fatalf("envInt(7) = %d, want 7", got)
	}
}
