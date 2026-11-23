package service

import (
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wricardo/drbrain/games/motor/game/engine"
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

	// GuestName is shown for a session that belongs to no account.
	GuestName = "Guest"
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
	// AccountID is the signed-in viewer: their entry is reported as Yours and
	// flagged Mine ("" = not signed in).
	AccountID string
}

// LeaderboardEntry is one ranked result. Entries that tie on both slots and
// steps share a rank (1, 2, 2, 4).
type LeaderboardEntry struct {
	Rank        int
	SessionID   string
	DisplayName string
	// Handle is the account's handle when the entry belongs to a signed-in
	// player with a public profile; "" for guests and private profiles.
	Handle string
	// Mine is true when the entry belongs to the viewing account.
	Mine  bool
	Slots int
	Steps int
	At    time.Time
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

// parseOrder normalises an order name; "" is the default.
func parseOrder(s string) (string, error) {
	switch o := strings.ToUpper(s); o {
	case "":
		return OrderSlots, nil
	case OrderSlots, OrderSteps:
		return o, nil
	}
	return "", Errorf(CodeInvalidArgument, "unknown order %q (want %s or %s)", s, OrderSlots, OrderSteps)
}

// boardRow is one session's recorded win under an ordering.
type boardRow struct {
	id    string
	mapID string
	win   Win
	rank  int
	// accountID is the owning account ("" = guest row); acct is that account,
	// nil when it no longer exists (such a row counts as a guest row).
	accountID string
	acct      *Account
}

// rankedBoards scans the live sessions once and returns, per map, the ranked
// rows of the sessions that won it under order. If onlyMap is set only that
// map is ranked. Only sessions that played a map's current rules count (see
// engine.Map.Fingerprint); maps that no longer exist have no board. Rows are
// sorted by the ordering, then earliest win, then session id; ties share a
// rank.
//
// Every number was recorded by the server when the run was won, so nothing
// here can be forged by a client. Each session lock is held only to copy a few
// small values; there is no I/O.
//
// Rows owned by an account are collapsed to one per account per map: the
// account's best under the ordering. Guest rows stay one per session.
func (svc *gameService) rankedBoards(order, onlyMap string) map[string][]boardRow {
	type fpRow struct {
		boardRow
		fp string
	}
	var all []fpRow
	for _, s := range svc.sessions.List() {
		s.Lock()
		if onlyMap == "" || s.MapID == onlyMap {
			w := s.FewestSlots
			if order == OrderSteps {
				w = s.FewestSteps
			}
			// s.Map is immutable after creation.
			if w != nil {
				all = append(all, fpRow{boardRow{id: s.ID, mapID: s.MapID, win: *w, accountID: s.AccountID}, s.Map.Fingerprint()})
			}
		}
		s.Unlock()
	}

	accounts := make(map[string]*Account)
	current := make(map[string]string) // map id -> current fingerprint ("" = no such map)
	byMap := make(map[string][]boardRow)
	for _, r := range all {
		fp, ok := current[r.mapID]
		if !ok {
			if m, err := svc.maps.Get(r.mapID); err == nil {
				fp = m.Fingerprint()
			}
			current[r.mapID] = fp
		}
		if fp != "" && r.fp == fp {
			row := r.boardRow
			if row.acct = svc.accountFor(row.accountID, accounts); row.acct == nil {
				row.accountID = "" // deleted or unknown account: a guest row
			}
			byMap[r.mapID] = append(byMap[r.mapID], row)
		}
	}
	for id, rows := range byMap {
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
		// One entry per account: the first (best) row of each account wins.
		seen := make(map[string]bool)
		kept := rows[:0]
		for _, r := range rows {
			if r.accountID != "" {
				if seen[r.accountID] {
					continue
				}
				seen[r.accountID] = true
			}
			kept = append(kept, r)
		}
		rows = kept
		rank := 0
		for i := range rows {
			if i == 0 || rows[i].win.key(order) != rows[i-1].win.key(order) {
				rank = i + 1
			}
			rows[i].rank = rank
		}
		byMap[id] = rows
	}
	return byMap
}

// entryOf converts a row for the API. Account rows show the account's display
// name and handle, or "Private player" without a handle when the profile is
// private; a row owned by no account is shown as "Guest". viewer is the
// signed-in account id ("" = none).
func (svc *gameService) entryOf(r boardRow, viewer string) LeaderboardEntry {
	e := LeaderboardEntry{
		Rank: r.rank, SessionID: r.id, DisplayName: GuestName,
		Slots: r.win.Slots, Steps: r.win.Steps, At: r.win.At,
	}
	if r.acct != nil {
		e.DisplayName, e.Handle = publicAccountName(r.acct, svc.opts.MaxDisplayName)
		e.Mine = viewer != "" && r.accountID == viewer
	}
	return e
}

// GetLeaderboard ranks the winning sessions of a map. A map that no longer
// exists is NOT_FOUND.
func (svc *gameService) GetLeaderboard(q LeaderboardQuery) (*Leaderboard, error) {
	order, err := parseOrder(q.Order)
	if err != nil {
		return nil, err
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
	if e, err := svc.maps.Entry(q.MapID); err != nil {
		return nil, err
	} else if !e.VisibleTo(q.AccountID) {
		return nil, Errorf(CodeNotFound, "map %q not found", q.MapID)
	}

	// The session's own account (if any), so a browser that remembers a session
	// still finds the account's single collapsed entry.
	sessAcct := ""
	if q.SessionID != "" {
		if s, ok := svc.sessions.Get(q.SessionID); ok {
			s.Lock()
			sessAcct = s.AccountID
			s.Unlock()
		}
	}
	rows := svc.rankedBoards(order, q.MapID)[q.MapID]
	lb := &Leaderboard{MapID: q.MapID, Order: order, Total: len(rows), Entries: []LeaderboardEntry{}}
	viewerRank := -1
	for i, r := range rows {
		if i < limit {
			lb.Entries = append(lb.Entries, svc.entryOf(r, q.AccountID))
		}
		switch {
		case q.AccountID != "" && r.accountID == q.AccountID:
			e := svc.entryOf(r, q.AccountID)
			lb.Yours, viewerRank = &e, i
		case viewerRank < 0 && q.SessionID != "" && (r.id == q.SessionID || (sessAcct != "" && r.accountID == sessAcct)):
			e := svc.entryOf(r, q.AccountID)
			lb.Yours = &e
		}
	}
	return lb, nil
}

// MaxRunSessionIDs bounds RunsQuery.SessionIDs (the number of sessions a
// browser remembers as its own).
const MaxRunSessionIDs = 200

// RunsQuery selects a player's sessions: an explicit set of session ids (the
// sessions one browser created) and/or the viewer's account.
//
// With SessionIDs the match is exact; if AccountID (the signed-in viewer) is
// also set the account's own entries are included, so a signed-in player sees
// both their profile's runs and the guest sessions of this browser. With
// neither, a signed-in viewer gets just their account's runs.
type RunsQuery struct {
	SessionIDs []string
	AccountID  string
	// ViewerID is who is asking. A draft map is omitted unless this is its
	// owner; an empty value hides every draft.
	ViewerID string
}

// PlayerRun is a player's best result on one map: the best of their winning
// sessions on the SLOTS board, with its current rank there.
type PlayerRun struct {
	MapID string
	// Entry is the winning session's row (Entry.Rank is its rank on the SLOTS board).
	Entry LeaderboardEntry
	// Total is the number of ranked sessions on that board.
	Total int
}

// GetPlayerRuns returns the best run per map (maps never won are absent) for
// the selected sessions, ordered by map id. Errors: INVALID_ARGUMENT (no
// selector, too many ids).
func (svc *gameService) GetPlayerRuns(q RunsQuery) ([]PlayerRun, error) {
	byID := len(q.SessionIDs) > 0
	switch {
	case !byID && q.AccountID == "":
		return nil, Errorf(CodeInvalidArgument, "give sessionIds or sign in")
	case len(q.SessionIDs) > MaxRunSessionIDs:
		return nil, Errorf(CodeInvalidArgument, "too many sessionIds (%d, max %d)", len(q.SessionIDs), MaxRunSessionIDs)
	}
	ids := make(map[string]bool, len(q.SessionIDs))
	for _, id := range q.SessionIDs {
		ids[id] = true
	}
	mine := func(r boardRow) bool {
		if q.AccountID != "" && r.acct != nil && r.accountID == q.AccountID {
			return true
		}
		return ids[r.id]
	}
	return svc.playerRuns(mine, q.AccountID, q.ViewerID), nil
}

// playerRuns picks, per map, the best SLOTS-board row that mine accepts. Draft
// maps are included only when viewer is their owner; a map deleted from the
// store keeps its historical runs visible.
func (svc *gameService) playerRuns(mine func(boardRow) bool, viewer, viewerID string) []PlayerRun {
	boards := svc.rankedBoards(OrderSlots, "")
	out := []PlayerRun{}
	for mapID, rows := range boards {
		if e, err := svc.maps.Entry(mapID); err == nil && !e.VisibleTo(viewerID) {
			continue
		}
		for _, r := range rows { // best first
			if mine(r) {
				out = append(out, PlayerRun{MapID: mapID, Entry: svc.entryOf(r, viewer), Total: len(rows)})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MapID < out[j].MapID })
	return out
}

// PublicName turns a stored account display name into what a leaderboard shows:
// control and formatting characters become spaces, runs of whitespace
// collapse, the result is cut to max characters, and nothing left means
// "Guest".
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
		return GuestName
	}
	return out
}
