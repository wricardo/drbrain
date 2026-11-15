package service

import (
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wricardo/drbrain-motor-programming/game/engine"
)

// Leaderboard orderings.
const (
	// OrderSlots ranks by fewest slots used, then fewest steps (the default).
	OrderSlots = "SLOTS"
	// OrderSteps ranks by fewest steps, then fewest slots.
	OrderSteps = "STEPS"
)

const (
	// DefaultLeaderboardLimit and MaxLeaderboardLimit bound GetLeaderboard.
	DefaultLeaderboardLimit = 20
	MaxLeaderboardLimit     = 100

	// AnonymousName is shown for a session without a display name.
	AnonymousName = "Anonymous"
)

// Win is a result recorded by the server at the moment a run was won: the
// number of non-EMPTY slots of the winning program and the steps the run took.
// Clients never supply it. A session keeps its best win under each ordering.
type Win struct {
	Slots int       `json:"slots"`
	Steps int       `json:"steps"`
	At    time.Time `json:"at"`
}

// beats reports whether w ranks strictly ahead of o under the given ordering.
// Equal results keep the earlier one, so a re-win never displaces a tie.
func (w Win) beats(o Win, order string) bool {
	a, b := w.key(order), o.key(order)
	if a != b {
		return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1])
	}
	return w.At.Before(o.At)
}

// key is the (primary, secondary) ranking pair under the ordering.
func (w Win) key(order string) [2]int {
	if order == OrderSteps {
		return [2]int{w.Steps, w.Slots}
	}
	return [2]int{w.Slots, w.Steps}
}

// recordWin notes the current (WON) VM as a candidate for the session's best
// results. Lock must be held and VM must be WON. A later, worse win never
// replaces a better one; an edited program that wins better does.
func (s *Session) recordWin(at time.Time) {
	w := Win{Slots: s.Program.SlotsUsed(), Steps: s.VM.Steps, At: at}
	if s.FewestSlots == nil || w.beats(*s.FewestSlots, OrderSlots) {
		c := w
		s.FewestSlots = &c
	}
	if s.FewestSteps == nil || w.beats(*s.FewestSteps, OrderSteps) {
		c := w
		s.FewestSteps = &c
	}
	if s.BestSteps == 0 || w.Steps < s.BestSteps {
		s.BestSteps = w.Steps
	}
}

// BackfillWin records the result of a session persisted before wins were
// tracked: a WON VM always belongs to the program stored with it (changing the
// program resets the VM), so the result can be recovered exactly. Persistence
// calls it when loading; it does nothing when a win is already recorded.
func (s *Session) BackfillWin() {
	if s.VM.Status == engine.StatusWon && s.FewestSlots == nil && s.FewestSteps == nil && s.VM.Steps > 0 {
		s.recordWin(s.LastActionAt)
	}
}

// LeaderboardQuery selects a board. Zero values pick the defaults.
type LeaderboardQuery struct {
	MapID string
	Order string // OrderSlots (default) or OrderSteps
	Limit int    // 0 = DefaultLeaderboardLimit, max MaxLeaderboardLimit
	// SessionID, when set, also reports that session's own entry and rank even
	// if it is outside the first Limit rows.
	SessionID string
}

// LeaderboardEntry is one ranked result. Entries that tie on both slots and
// steps share a rank (1, 2, 2, 4).
type LeaderboardEntry struct {
	Rank        int
	SessionID   string
	DisplayName string
	Slots       int
	Steps       int
	At          time.Time
}

// Leaderboard is the ranking of one map under one ordering.
type Leaderboard struct {
	MapID string
	Order string
	// Total is the number of ranked sessions, not just the rows returned.
	Total   int
	Entries []LeaderboardEntry
	// Yours is the entry of LeaderboardQuery.SessionID, or nil.
	Yours *LeaderboardEntry
}

// GetLeaderboard ranks the winning sessions of a map. The board is derived
// from the sessions themselves: every number was recorded by the server when
// the run was won, so nothing here can be forged by a client.
//
// Only sessions that played the map's current rules count (see
// engine.Map.Fingerprint): a session started before the map was edited keeps
// its own snapshot but is not comparable with the current puzzle. A map that
// no longer exists is NOT_FOUND.
func (svc *gameService) GetLeaderboard(q LeaderboardQuery) (*Leaderboard, error) {
	order := strings.ToUpper(q.Order)
	switch order {
	case "":
		order = OrderSlots
	case OrderSlots, OrderSteps:
	default:
		return nil, Errorf(CodeInvalidArgument, "unknown order %q (want %s or %s)", q.Order, OrderSlots, OrderSteps)
	}
	limit := q.Limit
	switch {
	case limit < 0:
		return nil, Errorf(CodeInvalidArgument, "limit must be >= 0")
	case limit == 0:
		limit = DefaultLeaderboardLimit
	case limit > MaxLeaderboardLimit:
		limit = MaxLeaderboardLimit
	}
	m, err := svc.maps.Get(q.MapID)
	if err != nil {
		return nil, err
	}
	fp := m.Fingerprint()

	type row struct {
		id, name string
		win      Win
	}
	var rows []row
	for _, s := range svc.sessions.List() {
		s.Lock()
		if s.MapID == q.MapID {
			w := s.FewestSlots
			if order == OrderSteps {
				w = s.FewestSteps
			}
			// s.Map is immutable after creation.
			if w != nil && s.Map.Fingerprint() == fp {
				rows = append(rows, row{id: s.ID, name: s.DisplayName, win: *w})
			}
		}
		s.Unlock()
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].win, rows[j].win
		if ka, kb := a.key(order), b.key(order); ka != kb {
			return ka[0] < kb[0] || (ka[0] == kb[0] && ka[1] < kb[1])
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return rows[i].id < rows[j].id
	})

	lb := &Leaderboard{MapID: q.MapID, Order: order, Total: len(rows), Entries: []LeaderboardEntry{}}
	rank := 0
	for i, r := range rows {
		if i == 0 || r.win.key(order) != rows[i-1].win.key(order) {
			rank = i + 1
		}
		e := LeaderboardEntry{
			Rank: rank, SessionID: r.id, DisplayName: PublicName(r.name, svc.opts.MaxDisplayName),
			Slots: r.win.Slots, Steps: r.win.Steps, At: r.win.At,
		}
		if i < limit {
			lb.Entries = append(lb.Entries, e)
		}
		if q.SessionID != "" && r.id == q.SessionID {
			c := e
			lb.Yours = &c
		}
	}
	return lb, nil
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
