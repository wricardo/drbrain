// Package engine derives Music Region challenges from a score: retrograde and
// inversion transforms, the solved check and seeded per-difficulty generation.
// It has no I/O, no globals unsafely shared, no time and no unseeded
// randomness; everything is a pure function of its arguments.
package engine

import (
	"fmt"
	"strings"
)

// Difficulty selects the required flips and the reward. Every difficulty
// challenges the whole piece: the bars are always the same, only the transforms
// the player must undo differ.
type Difficulty int

const (
	Novice Difficulty = iota + 1
	Expert
	Genius
)

// String returns the wire and display name.
func (d Difficulty) String() string {
	switch d {
	case Novice:
		return "novice"
	case Expert:
		return "expert"
	case Genius:
		return "genius"
	}
	return "unknown"
}

// ParseDifficulty accepts the case-insensitive wire names.
func ParseDifficulty(s string) (Difficulty, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "novice":
		return Novice, nil
	case "expert":
		return Expert, nil
	case "genius":
		return Genius, nil
	}
	return 0, fmt.Errorf("engine: unknown difficulty %q", s)
}

// PercentPerWin is the progress each cleared puzzle awards.
func (d Difficulty) PercentPerWin() int {
	switch d {
	case Novice:
		return 5
	case Expert:
		return 10
	case Genius:
		return 15
	}
	return 0
}

// valid reports whether d is one of the three named difficulties.
func (d Difficulty) valid() bool {
	switch d {
	case Novice, Expert, Genius:
		return true
	}
	return false
}
