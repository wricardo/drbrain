package service

import (
	"math/rand/v2"
	"unicode/utf8"
)

// Guest names are "<Adjective><Noun>" ("BraveOtter"). A session created without
// a display name gets one, so every leaderboard row has a friendly name and a
// player who never types anything still has a stable-looking identity. The
// lists are curated: short, cheerful, family friendly, one word each, ASCII
// letters only. Names are not unique and are not an identity (a session id is);
// with len(guestAdjectives)*len(guestNouns) combinations a clash is rare and
// harmless, so no digits are appended.
var guestAdjectives = []string{
	"Brave", "Bright", "Calm", "Clever", "Cosmic", "Curious", "Daring", "Dizzy",
	"Eager", "Fancy", "Fluffy", "Gentle", "Giddy", "Golden", "Happy", "Jolly",
	"Keen", "Kind", "Lively", "Lucky", "Merry", "Mighty", "Nimble", "Peppy",
	"Plucky", "Proud", "Quick", "Quiet", "Rapid", "Rosy", "Shiny", "Silly",
	"Sleepy", "Snappy", "Sunny", "Swift", "Tiny", "Wise", "Witty", "Zesty",
}

var guestNouns = []string{
	"Badger", "Beaver", "Bunny", "Cactus", "Comet", "Cricket", "Dolphin", "Dragon",
	"Falcon", "Ferret", "Firefly", "Fox", "Gecko", "Hedgehog", "Heron", "Kitten",
	"Koala", "Lemur", "Llama", "Meteor", "Moose", "Narwhal", "Octopus", "Otter",
	"Owl", "Panda", "Parrot", "Pebble", "Penguin", "Pickle", "Puffin", "Quokka",
	"Rocket", "Sparrow", "Squirrel", "Tiger", "Toucan", "Turtle", "Walrus", "Wombat",
}

// GuestName returns a random "<Adjective><Noun>" name of at most max
// characters (max <= 0 means no limit).
func GuestName(max int) string {
	name := guestAdjectives[rand.IntN(len(guestAdjectives))] + guestNouns[rand.IntN(len(guestNouns))]
	if max > 0 && utf8.RuneCountInString(name) > max {
		name = string([]rune(name)[:max])
	}
	return name
}
