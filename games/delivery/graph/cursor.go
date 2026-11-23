package graph

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/wricardo/drbrain/games/delivery/game/session"
	"github.com/wricardo/drbrain/games/delivery/graph/model"
)

// sessionCursor is the opaque page position encoded into every session row's
// `cursor`. It carries the sort it was built for, the sort key of that row,
// and the row id (the sort tiebreak), so the next page resumes exactly after
// it even when many rows share a key.
type sessionCursor struct {
	Sort  string `json:"s"`
	Value int64  `json:"v"`
	ID    string `json:"i"`
}

// encodeSessionCursor builds the opaque cursor for one row.
func encodeSessionCursor(sort session.SessionSort, row *model.SessionSummary) string {
	c := sessionCursor{Sort: sessionSortName(sort), Value: cursorValue(sort, row), ID: row.ID}
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeSessionCursor validates and decodes a client cursor for the sort the
// caller asked for. A cursor from another sort is a client bug, not a page.
func decodeSessionCursor(raw string, sort session.SessionSort) (session.ListCursor, error) {
	blob, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return session.ListCursor{}, fmt.Errorf("invalid cursor")
	}
	var c sessionCursor
	if err := json.Unmarshal(blob, &c); err != nil {
		return session.ListCursor{}, fmt.Errorf("invalid cursor")
	}
	if c.Sort != sessionSortName(sort) {
		return session.ListCursor{}, fmt.Errorf("cursor belongs to sort %s, not %s", c.Sort, sessionSortName(sort))
	}
	if !session.ValidID(c.ID) {
		return session.ListCursor{}, fmt.Errorf("invalid cursor")
	}
	return session.ListCursor{Value: c.Value, ID: c.ID}, nil
}

// cursorValue is the sort key a row is ordered by: creation time in unix
// nanos, tick, or money.
func cursorValue(sort session.SessionSort, row *model.SessionSummary) int64 {
	switch sort {
	case session.SortTick:
		return int64(row.Tick)
	case session.SortMoney:
		return int64(row.Money)
	default:
		return row.CreatedAt.UnixNano()
	}
}

// sessionSortName is the GraphQL enum name, used inside the cursor so a cursor
// cannot silently be reused with a different sort.
func sessionSortName(sort session.SessionSort) string {
	switch sort {
	case session.SortTick:
		return "TICK"
	case session.SortMoney:
		return "MONEY"
	default:
		return "CREATED_AT"
	}
}
