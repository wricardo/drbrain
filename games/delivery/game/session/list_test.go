package session

import (
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// countingStore is an in-memory sessionstore.Store that counts payload reads,
// so a test can prove the list path does not decode saves it can describe from
// meta alone.
type countingStore struct {
	mu       sync.Mutex
	metas    map[string]sessionstore.Meta
	payloads map[string][]byte
	loads    int
}

func newCountingStore() *countingStore {
	return &countingStore{
		metas:    map[string]sessionstore.Meta{},
		payloads: map[string][]byte{},
	}
}

func (s *countingStore) Put(meta sessionstore.Meta, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metas[meta.ID] = meta
	s.payloads[meta.ID] = payload
	return nil
}

func (s *countingStore) Get(id string) (sessionstore.Meta, []byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.metas[id]
	if !ok {
		return sessionstore.Meta{}, nil, false
	}
	return meta, s.payloads[id], true
}

func (s *countingStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.metas[id]; !ok {
		return sessionstore.ErrNotFound
	}
	delete(s.metas, id)
	delete(s.payloads, id)
	return nil
}

func (s *countingStore) List() ([]sessionstore.Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sessionstore.Meta, 0, len(s.metas))
	for _, m := range s.metas {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *countingStore) Persist(meta sessionstore.Meta, payload []byte) error {
	return s.Put(meta, payload)
}

func (s *countingStore) Load(id string) (sessionstore.Meta, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.metas[id]
	if !ok {
		return sessionstore.Meta{}, nil, sessionstore.ErrNotFound
	}
	s.loads++
	return meta, s.payloads[id], nil
}

func (s *countingStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.metas)
}

func (s *countingStore) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}

// Test account ids: the session layer keeps only 24-character lower-case hex,
// so a test that used "acct-1" would silently look like a guest row.
const (
	acctOne   = "aaaaaaaaaaaaaaaaaaaaaaaa"
	acctTwo   = "bbbbbbbbbbbbbbbbbbbbbbbb"
	acctSeven = "cccccccccccccccccccccccc"
)

// stubCatalog serves DefaultID for the list path plus one real city for the
// reload test, which genuinely needs the city config.
type stubCatalog struct {
	cfg  *engine.CityConfig
	city *engine.City
}

func (c stubCatalog) DefaultID() string { return "city" }

func (c stubCatalog) Config(string, string) (*engine.CityConfig, error) {
	if c.cfg == nil {
		return nil, errors.New("stub catalog: no city configured")
	}
	return c.cfg, nil
}

// addStored registers a save. withCounters=false writes the meta shape every
// save had before counters existed, which forces the legacy decode path.
func (s *countingStore) addStored(t *testing.T, id string, created time.Time, tick int64, money int, account string, withCounters bool) {
	t.Helper()
	meta := sessionstore.Meta{ID: id, CreatedAt: created, AccountID: account}
	if withCounters {
		meta.Counters = &sessionstore.Counters{Tick: tick, Money: money}
	}
	payload := []byte(`{"id":"` + id + `","world":{"tick":` + itoa(tick) + `}}`)
	if err := s.Put(meta, payload); err != nil {
		t.Fatal(err)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// TestListDescribesStoredSavesFromCounters is the F1 guarantee: a bounded list
// with the default order decodes no payloads at all when counters are present,
// and only the rows that survive truncation when they are not.
func TestListDescribesStoredSavesFromCounters(t *testing.T) {
	store := newCountingStore()
	mgr := &Manager{catalog: stubCatalog{}, store: store, sessions: map[string]*Session{}}
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store.addStored(t, "aaaa", base.Add(3*time.Hour), 300, 900, acctOne, true)
	store.addStored(t, "bbbb", base.Add(2*time.Hour), 200, 400, acctOne, true)
	store.addStored(t, "cccc", base.Add(1*time.Hour), 100, 100, "", true)

	rows := mgr.List(ListOptions{Limit: 2})
	if store.loadCount() != 0 {
		t.Fatalf("payload loads = %d, want 0: counters must describe the row", store.loadCount())
	}
	if len(rows) != 2 || rows[0].ID != "aaaa" || rows[1].ID != "bbbb" {
		t.Fatalf("rows = %+v, want the two newest", rows)
	}
	if rows[0].Tick != 300 || rows[0].Money != 900 {
		t.Fatalf("counters not applied: %+v", rows[0])
	}

	// A legacy row (no counters) still decodes, but only after truncation, so
	// a limit of 1 costs one payload read at most.
	store.addStored(t, "dddd", base.Add(4*time.Hour), 500, 50, "", false)
	before := store.loadCount()
	rows = mgr.List(ListOptions{Limit: 1})
	if len(rows) != 1 || rows[0].ID != "dddd" {
		t.Fatalf("rows = %+v, want the legacy row", rows)
	}
	if got := store.loadCount() - before; got != 1 {
		t.Fatalf("legacy loads = %d, want exactly 1", got)
	}
	if rows[0].Tick != 500 {
		t.Fatalf("legacy row tick = %d, want 500", rows[0].Tick)
	}
}

// TestListSortsAndFilters covers the ordering keys and the owner filter.
func TestListSortsAndFilters(t *testing.T) {
	store := newCountingStore()
	mgr := &Manager{catalog: stubCatalog{}, store: store, sessions: map[string]*Session{}}
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store.addStored(t, "aaaa", base.Add(3*time.Hour), 300, 900, acctOne, true)
	store.addStored(t, "bbbb", base.Add(2*time.Hour), 200, 400, acctTwo, true)
	store.addStored(t, "cccc", base.Add(1*time.Hour), 100, 100, acctOne, true)

	byTick := mgr.List(ListOptions{Sort: SortTick})
	if byTick[0].ID != "aaaa" || byTick[2].ID != "cccc" {
		t.Fatalf("tick order = %+v", byTick)
	}
	byMoney := mgr.List(ListOptions{Sort: SortMoney})
	if byMoney[0].ID != "aaaa" || byMoney[0].Money != 900 {
		t.Fatalf("money order = %+v", byMoney)
	}
	mine := mgr.List(ListOptions{AccountID: acctOne})
	if len(mine) != 2 {
		t.Fatalf("mine = %+v, want two", mine)
	}
	for _, r := range mine {
		if r.AccountID != acctOne {
			t.Fatalf("mine returned %+v", r)
		}
	}
	paged := mgr.List(ListOptions{Before: base.Add(2 * time.Hour)})
	if len(paged) != 1 || paged[0].ID != "cccc" {
		t.Fatalf("before cursor = %+v, want only cccc", paged)
	}
}

// TestSavePublishesCountersAndName pins what a save writes into the platform
// meta: the counters the list reads, the owner, and the player's name.
func TestSavePublishesCountersAndName(t *testing.T) {
	city, cfg := testCity(t)
	store := newCountingStore()
	s, err := newWithStore("savename", city, cfg, "city", 1, store, acctSeven, "hash")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.Do(func(w *engine.World) { w.Tick = 42; w.Ledger.Money = 777 })
	s.Rename("  My Fleet  ")
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	meta, _, ok := store.Get("savename")
	if !ok {
		t.Fatal("save missing from the store")
	}
	if meta.DisplayName != "My Fleet" {
		t.Fatalf("display name = %q, want trimmed My Fleet", meta.DisplayName)
	}
	if meta.AccountID != acctSeven {
		t.Fatalf("account = %q", meta.AccountID)
	}
	if meta.Counters == nil || meta.Counters.Tick != 42 || meta.Counters.Money != 777 {
		t.Fatalf("counters = %+v", meta.Counters)
	}

	// The list describes this row from meta alone.
	before := store.loadCount()
	rows := (&Manager{catalog: stubCatalog{}, store: store, sessions: map[string]*Session{}}).List(ListOptions{})
	if store.loadCount() != before {
		t.Fatal("list decoded a payload that had counters")
	}
	if len(rows) != 1 || rows[0].DisplayName != "My Fleet" || rows[0].Tick != 42 {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRenameSurvivesReload proves the name is part of the payload, not only the
// meta, so a manager that loses its meta still shows it.
func TestRenameSurvivesReload(t *testing.T) {
	city, cfg := testCity(t)
	store := newCountingStore()
	s, err := newWithStore("renameload", city, cfg, "city", 1, store, "", "")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.Rename("Renamed Save")
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.Close()

	mgr := &Manager{catalog: stubCatalog{cfg: cfg}, store: store, sessions: map[string]*Session{}}
	loaded, err := mgr.GetOrLoad("renameload")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer loaded.Close()
	if got := loaded.DisplayName(); got != "Renamed Save" {
		t.Fatalf("display name after reload = %q", got)
	}
	// The snapshot must carry it too: a rename that only reached the list left
	// the mutation response and session(id) showing an empty name.
	if got := loaded.Snapshot().DisplayName; got != "Renamed Save" {
		t.Fatalf("snapshot display name = %q", got)
	}
}

// TestStreamCarriesTheSameStatementWindow pins F4: the live frame and the query
// return the same statement rows for the same tick, so a subscriber never sees
// a narrower account than a reader.
func TestStreamCarriesTheSameStatementWindow(t *testing.T) {
	s := newTestSession(t)
	seedStatement(t, s)

	full := s.Snapshot()
	stream := s.StreamSnapshot()
	if len(full.Ledger.CashEntries) != len(stream.Ledger.CashEntries) {
		t.Fatalf("query rows = %d, stream rows = %d: the windows must match",
			len(full.Ledger.CashEntries), len(stream.Ledger.CashEntries))
	}
	for i := range full.Ledger.CashEntries {
		if full.Ledger.CashEntries[i].Seq != stream.Ledger.CashEntries[i].Seq {
			t.Fatalf("row %d differs: query %d, stream %d",
				i, full.Ledger.CashEntries[i].Seq, stream.Ledger.CashEntries[i].Seq)
		}
	}
	if len(full.Ledger.Months) != len(stream.Ledger.Months) {
		t.Fatalf("months differ: query %d, stream %d", len(full.Ledger.Months), len(stream.Ledger.Months))
	}
}

// TestListDefaultOrderPreservesLiveAndLegacyRows is the regression for a list
// that mixed a live session with stored saves: legacy rows were decoded by
// their pre-sort row index, so sorting could overwrite a different session and
// drop the live one. Every id must appear exactly once.
func TestListDefaultOrderPreservesLiveAndLegacyRows(t *testing.T) {
	city, cfg := testCity(t)
	store := newCountingStore()
	live, err := newWithStore("live1", city, cfg, "city", 1, store, "", "hash")
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	live.CreatedAt = base.Add(time.Hour)
	mgr := &Manager{catalog: stubCatalog{}, store: store, sessions: map[string]*Session{"live1": live}}
	store.addStored(t, "legacy1", base.Add(3*time.Hour), 500, 50, "", false)
	store.addStored(t, "newer1", base.Add(2*time.Hour), 200, 400, "", true)

	rows := mgr.List(ListOptions{})
	want := []string{"legacy1", "newer1", "live1"}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %d", rows, len(want))
	}
	for i, id := range want {
		if rows[i].ID != id {
			t.Fatalf("row %d = %q, want %q (rows %+v)", i, rows[i].ID, id, rows)
		}
	}
	if rows[0].Tick != 500 {
		t.Fatalf("legacy row counters = %d, want 500", rows[0].Tick)
	}
}

// TestListCursorPagesExactOrdering covers the compound cursor: ordering is key
// descending with id ascending on ties, so equal ticks (and equal creation
// times) page without duplicates or gaps.
func TestListCursorPagesExactOrdering(t *testing.T) {
	store := newCountingStore()
	mgr := &Manager{catalog: stubCatalog{}, store: store, sessions: map[string]*Session{}}
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store.addStored(t, "aaa", base, 5, 0, "", true)
	store.addStored(t, "bbb", base, 5, 0, "", true)
	store.addStored(t, "ccc", base, 3, 0, "", true)

	tickPage := func(cursor *ListCursor) []string {
		rows := mgr.List(ListOptions{Sort: SortTick, Limit: 10, Cursor: cursor})
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if got := tickPage(nil); len(got) != 3 || got[0] != "aaa" || got[1] != "bbb" || got[2] != "ccc" {
		t.Fatalf("tick order = %v, want [aaa bbb ccc]", got)
	}
	if got := tickPage(&ListCursor{Value: 5, ID: "aaa"}); len(got) != 2 || got[0] != "bbb" || got[1] != "ccc" {
		t.Fatalf("page after aaa = %v, want [bbb ccc]", got)
	}

	// Equal creation times must page by id, not drop the tie.
	created := func(cursor *ListCursor) []string {
		rows := mgr.List(ListOptions{Sort: SortCreatedAt, Limit: 10, Cursor: cursor})
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}
	all := created(nil)
	if len(all) != 3 {
		t.Fatalf("created order = %v", all)
	}
	first := all[0]
	rest := created(&ListCursor{Value: base.UnixNano(), ID: first})
	if len(rest) != 2 || contains(rest, first) {
		t.Fatalf("created page after %s = %v", first, rest)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
