package account

import (
	"crypto/rand"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wricardo/drbrain/platform/errs"
)

// AnonymousName is shown for a session without a display name.
const AnonymousName = "Anonymous"

// Handle limits. A handle is the public, unique name of an account ("/u/<handle>").
const (
	MinHandleLen = 3
	MaxHandleLen = 20
)

var handleRE = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// reservedHandles can never be taken: they would look official, collide with
// routes, or confuse the UI.
var reservedHandles = map[string]bool{
	"admin": true, "administrator": true, "root": true, "system": true, "support": true, "staff": true,
	"moderator": true, "mod": true, "owner": true, "official": true, "help": true, "security": true,
	"api": true, "www": true, "auth": true, "login": true, "logout": true, "signin": true, "signup": true,
	"settings": true, "profile": true, "account": true, "accounts": true, "me": true, "you": true,
	"null": true, "undefined": true, "anonymous": true, "guest": true, "everyone": true, "nobody": true,
	"drbrain": true, "drbrain3": true, "wricardo": true, "motorprogramming": true, "motor_programming": true,
	"play": true, "watch": true, "maps": true, "runs": true, "leaderboard": true, "sessions": true,
	"editor": true, "learn": true, "graphql": true, "playground": true, "static": true, "llms": true,
}

// blockedStems are fragments that may not appear in a handle or display name
// (after lower-casing, de-leeting and dropping separators). It is a small
// starter list for a game children may play, not a complete filter: extend it
// when something slips through. Fragments are chosen to be distinctive so that
// ordinary words are not caught.
var blockedStems = []string{
	"fuck", "shit", "bitch", "cunt", "whore", "slut", "nigg", "fagg", "faggot", "retard", "rapist",
	"nazi", "hitler", "porn", "penis", "vagina", "dildo", "blowjob", "handjob", "pedophil", "molest",
	"cocksuck", "motherfuck", "bastard", "asshole", "dickhead", "wanker", "tranny", "kkk", "jihad",
	"suicide", "killyourself",
}

var leetReplacer = strings.NewReplacer("0", "o", "1", "i", "3", "e", "4", "a", "5", "s", "7", "t", "8", "b", "@", "a", "$", "s", "!", "i")

// squash lower-cases s, maps common look-alike characters to letters and drops
// everything that is not a letter, so "F_u-C.k" and "fuck" compare equal.
func squash(s string) string {
	s = leetReplacer.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Blocked reports whether text contains a blocked fragment.
func Blocked(text string) bool {
	q := squash(text)
	for _, stem := range blockedStems {
		if strings.Contains(q, stem) {
			return true
		}
	}
	return false
}

// NormalizeHandle lower-cases and trims a handle as typed by a player.
func NormalizeHandle(h string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "@")))
}

// ValidateHandle checks the format, the reserved list and the blocklist of an
// already normalised handle.
func ValidateHandle(h string) error {
	switch {
	case !handleRE.MatchString(h):
		return errs.Errorf(errs.CodeInvalidArgument, "handles are %d-%d characters: lowercase letters, digits and underscores", MinHandleLen, MaxHandleLen)
	case reservedHandles[h]:
		return errs.Errorf(errs.CodeInvalidArgument, "that handle is reserved")
	case Blocked(h):
		return errs.Errorf(errs.CodeInvalidArgument, "that handle is not allowed")
	}
	return nil
}

// CleanAccountDisplayName applies the public-name cleaning used on boards
// (PublicName) plus the blocklist. Empty means "use the handle".
func CleanAccountDisplayName(name string, max int) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(name)); max > 0 && n > max {
		return "", errs.Errorf(errs.CodeInvalidArgument, "display name too long (%d characters, max %d)", n, max)
	}
	clean := PublicName(name, max)
	if clean == AnonymousName && !strings.EqualFold(strings.TrimSpace(name), AnonymousName) {
		return "", nil
	}
	if Blocked(clean) {
		return "", errs.Errorf(errs.CodeInvalidArgument, "that name is not allowed")
	}
	return clean, nil
}

// SuggestHandle returns a random "<adjective><noun>" handle, with a numeric
// suffix when attempt > 0 so a retry after a clash is different. The result
// always passes ValidateHandle.
func SuggestHandle(attempt int) string {
	h := strings.ToLower(guestAdjectives[randIntn(len(guestAdjectives))] + guestNouns[randIntn(len(guestNouns))])
	if attempt > 0 {
		digits := 2
		if attempt > 3 {
			digits = 4
		}
		lim := 1
		for i := 0; i < digits; i++ {
			lim *= 10
		}
		h += strconv.Itoa(randIntn(lim))
	}
	if len(h) > MaxHandleLen {
		h = h[:MaxHandleLen]
	}
	return h
}

func randIntn(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// PublicName turns a stored display name into what a leaderboard shows:
// control and formatting characters become spaces, runs of whitespace
// collapse, the result is cut to max characters, and nothing left means
// "Anonymous".
func PublicName(name string, max int) string {
	var b strings.Builder
	space := true // swallows leading whitespace
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	out := strings.TrimSpace(b.String())
	if max > 0 && utf8.RuneCountInString(out) > max {
		out = strings.TrimSpace(string([]rune(out)[:max]))
	}
	if out == "" {
		return AnonymousName
	}
	return out
}
