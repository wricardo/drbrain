package mapstore

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// openPG returns a PostgreSQL-backed store with edit tokens and unlimited maps.
func openPG(t *testing.T, pool *pgxpool.Pool, builtin string, publish func(toy) error) *Store[toy] {
	t.Helper()
	s, err := Open(Options[toy]{
		BuiltinDir:      builtin,
		Game:            "toy",
		Pool:            pool,
		Decode:          decodeToy,
		Encode:          encodeToy,
		Validate:        validateToy,
		PublishValidate: publish,
		IssueEditTokens: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPGDraftCreateReloadAndVisibility(t *testing.T) {
	pool := pgtestPool(t)
	builtin := t.TempDir()
	writeBuiltin(t, builtin, "alpha", toy{ID: "alpha", Value: 1})
	s := openPG(t, pool, builtin, nil)

	token, err := s.CreateDraft("mine", toy{ID: "mine", Value: 2}, "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("CreateDraft returned no token with IssueEditTokens")
	}
	got, err := s.Get("mine")
	if err != nil {
		t.Fatal(err)
	}
	if got.Builtin || got.OwnerID != "owner-a" || got.Status != StatusDraft || got.Config.Value != 2 {
		t.Fatalf("Get draft = %+v", got)
	}

	// A second store on the same game sees the durable row.
	other := openPG(t, pool, builtin, nil)
	if got, err := other.Get("mine"); err != nil || got.OwnerID != "owner-a" {
		t.Fatalf("reload = %+v, %v", got, err)
	}

	// Publish a second owner's map to compare visibility.
	if err := publishValid(t, other, "theirs", "owner-b"); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name   string
		list   []Map[toy]
		wanted []string
	}{
		{"all", mustList(t, s.List), []string{"alpha", "mine", "theirs"}},
		{"owner-a", mustList(t, func() ([]Map[toy], error) { return s.ListFor("owner-a") }), []string{"alpha", "mine", "theirs"}},
		{"owner-b", mustList(t, func() ([]Map[toy], error) { return s.ListFor("owner-b") }), []string{"alpha", "theirs"}},
		{"anonymous", mustList(t, func() ([]Map[toy], error) { return s.ListFor("") }), []string{"alpha", "theirs"}},
	} {
		if names := mapNamesOf(tt.list); fmt.Sprint(names) != fmt.Sprint(tt.wanted) {
			t.Errorf("%s ListFor = %v, want %v", tt.name, names, tt.wanted)
		}
	}
	if list := mustList(t, s.List); !list[0].Builtin || list[0].Name != "alpha" {
		t.Errorf("List should start with the builtin: %+v", list)
	}
}

func TestPGRefusesBuiltinShadowAndEmptyOwner(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	writeBuiltin(t, s.builtinDir, "alpha", toy{ID: "alpha", Value: 1})
	if _, err := s.CreateDraft("alpha", toy{ID: "alpha", Value: 2}, "o"); !errors.Is(err, ErrExists) {
		t.Fatalf("draft shadowing builtin = %v", err)
	}
	if _, err := s.Create("alpha", toy{ID: "alpha", Value: 2}); !errors.Is(err, ErrExists) {
		t.Fatalf("legacy create shadowing builtin = %v", err)
	}
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 2}, "  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("draft with empty owner = %v", err)
	}
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 0}, "o"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("structurally invalid draft = %v", err)
	}
}

func TestPGBuiltinAdminGate(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	writeBuiltin(t, s.builtinDir, "alpha", toy{ID: "alpha", Value: 1})

	if _, err := s.SaveOwned("alpha", toy{ID: "alpha", Value: 2}, Actor{AccountID: "o"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("non-admin builtin save = %v", err)
	}
	if err := s.DeleteOwned("alpha", Actor{AccountID: "o"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("non-admin builtin delete = %v", err)
	}
	got, err := s.SaveOwned("alpha", toy{ID: "alpha", Value: 2}, Actor{Admin: true})
	if err != nil || !got.Builtin || got.Status != StatusPublished {
		t.Fatalf("admin builtin save = %+v, %v", got, err)
	}
	if v, _ := s.Get("alpha"); v.Config.Value != 2 {
		t.Errorf("admin save not in the file: %+v", v)
	}
	if err := s.DeleteOwned("alpha", Actor{Admin: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted builtin = %v", err)
	}
}

func TestPGPublishValidationAndEditLifecycle(t *testing.T) {
	publish := func(v toy) error {
		if v.Value < 10 {
			return errors.New("too small to publish")
		}
		return nil
	}
	s := openPG(t, pgtestPool(t), t.TempDir(), publish)
	owner := Actor{AccountID: "owner-a"}
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 1}, owner.AccountID); err != nil {
		t.Fatal(err)
	}

	// Structural validation passes a map the publish validator rejects.
	if _, err := s.SaveOwned("mine", toy{ID: "mine", Value: 2}, owner); err != nil {
		t.Fatalf("structural save = %v", err)
	}
	if _, err := s.Publish("mine", owner); !errors.Is(err, ErrInvalid) {
		t.Fatalf("publish invalid = %v", err)
	}
	if got, _ := s.Get("mine"); got.Status != StatusDraft {
		t.Fatalf("failed publish changed status: %+v", got)
	}

	if _, err := s.SaveOwned("mine", toy{ID: "mine", Value: 11}, owner); err != nil {
		t.Fatal(err)
	}
	published, err := s.Publish("mine", owner)
	if err != nil || published.Status != StatusPublished {
		t.Fatalf("publish = %+v, %v", published, err)
	}
	// Publishing again is an idempotent no-op.
	if again, err := s.Publish("mine", owner); err != nil || again.Status != StatusPublished {
		t.Fatalf("republish = %+v, %v", again, err)
	}

	// An owner's edit of a published map returns it to draft.
	edited, err := s.SaveOwned("mine", toy{ID: "mine", Value: 12}, owner)
	if err != nil || edited.Status != StatusDraft || edited.OwnerID != owner.AccountID {
		t.Fatalf("owner edit = %+v, %v", edited, err)
	}
}

func TestPGAdminEditPreservesOwnerAndStatus(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	owner := Actor{AccountID: "owner-a"}
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 1}, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("mine", owner); err != nil {
		t.Fatal(err)
	}
	got, err := s.SaveOwned("mine", toy{ID: "mine", Value: 3}, Actor{Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != owner.AccountID || got.Status != StatusPublished {
		t.Fatalf("admin edit = %+v", got)
	}
	if err := s.DeleteOwned("mine", Actor{Admin: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPGOwnershipGates(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	owner := Actor{AccountID: "owner-a"}
	other := Actor{AccountID: "owner-b"}
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 1}, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveOwned("mine", toy{ID: "mine", Value: 2}, other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other save = %v", err)
	}
	if _, err := s.Publish("mine", other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other publish = %v", err)
	}
	if err := s.DeleteOwned("mine", other); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other delete = %v", err)
	}
	if err := s.DeleteOwned("mine", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("mine"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted draft = %v", err)
	}
	if _, err := s.SaveOwned("missing", toy{ID: "missing", Value: 1}, owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("save missing = %v", err)
	}
}

func TestPGClaimEditToken(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	token, err := s.Create("legacy", toy{ID: "legacy", Value: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("legacy", "owner-a", "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("bad token = %v", err)
	}
	if _, err := s.Claim("legacy", "owner-a", ""); !errors.Is(err, ErrEditTokenRequired) {
		t.Fatalf("empty token = %v", err)
	}
	got, err := s.Claim("legacy", "owner-a", token)
	if err != nil || got.OwnerID != "owner-a" || got.Status != StatusPublished {
		t.Fatalf("claim = %+v, %v", got, err)
	}
	if again, err := s.Claim("legacy", "owner-a", token); err != nil || again.OwnerID != "owner-a" {
		t.Fatalf("same-owner claim = %+v, %v", again, err)
	}
	if _, err := s.Claim("legacy", "owner-b", token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other-owner claim = %v", err)
	}
	if err := s.VerifyEditToken("legacy", token); err != nil {
		t.Fatalf("VerifyEditToken after claim = %v", err)
	}

	noToken := func() *Store[toy] {
		s, err := Open(Options[toy]{
			BuiltinDir: t.TempDir(), Game: "toy", Pool: s.pg,
			Decode: decodeToy, Encode: encodeToy, Validate: validateToy,
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}()
	if _, err := noToken.Create("plain", toy{ID: "plain", Value: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := noToken.Claim("plain", "owner-a", "anything"); !errors.Is(err, ErrEditTokenRequired) {
		t.Fatalf("claim without a stored token = %v", err)
	}
}

func TestPGDuplicateAndConcurrentCreate(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 1}, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDraft("mine", toy{ID: "mine", Value: 2}, "o"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate draft = %v", err)
	}

	const n = 10
	var wins, exists atomic.Int32
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := s.CreateDraft("race", toy{ID: "race", Value: 1}, "o"); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrExists):
				exists.Add(1)
			default:
				t.Errorf("concurrent create: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || exists.Load() != n-1 {
		t.Fatalf("wins=%d exists=%d", wins.Load(), exists.Load())
	}
}

func TestPGQuota(t *testing.T) {
	pool := pgtestPool(t)
	s, err := Open(Options[toy]{
		BuiltinDir: t.TempDir(), Game: "toy", Pool: pool,
		Decode: decodeToy, Encode: encodeToy, Validate: validateToy,
		MaxCustomMaps: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	const n = 10
	var wins, limited atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("m%02d", i)
			switch _, err := s.CreateDraft(name, toy{ID: name, Value: 1}, "o"); {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrLimit):
				limited.Add(1)
			default:
				t.Errorf("quota create: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 3 || limited.Load() != n-3 {
		t.Fatalf("wins=%d limited=%d", wins.Load(), limited.Load())
	}
}

func TestPGCorruptRowIsSkipped(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	if _, err := s.CreateDraft("good", toy{ID: "good", Value: 1}, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pg.Exec(t.Context(), `
INSERT INTO platform_maps (game, id, config, owner_id, status)
VALUES ('toy', 'broken', '{"id": "", "value": 0}'::jsonb, 'o', 'DRAFT')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("broken"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Get corrupt = %v", err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if names := mapNamesOf(list); fmt.Sprint(names) != "[good]" {
		t.Fatalf("List with corrupt row = %v", names)
	}
	if list, err := s.ListFor("o"); err != nil || len(list) != 1 {
		t.Fatalf("ListFor with corrupt row = %v, %v", mapNamesOf(list), err)
	}
}

func TestPGOwnerDeletionHelpers(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	for _, name := range []string{"draft-a", "pub-a"} {
		if _, err := s.CreateDraft(name, toy{ID: name, Value: 1}, "owner-a"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateDraft("draft-b", toy{ID: "draft-b", Value: 1}, "owner-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("pub-a", Actor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDraftsByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.OrphanPublishedByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("draft-a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("draft survived owner deletion: %v", err)
	}
	if got, err := s.Get("pub-a"); err != nil || got.OwnerID != "" || got.Status != StatusPublished {
		t.Errorf("orphaned published = %+v, %v", got, err)
	}
	if got, err := s.Get("draft-b"); err != nil || got.OwnerID != "owner-b" {
		t.Errorf("other owner's draft = %+v, %v", got, err)
	}
}

func TestPGLegacyWrappers(t *testing.T) {
	s := openPG(t, pgtestPool(t), t.TempDir(), nil)
	token, err := s.Create("old", toy{ID: "old", Value: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("old")
	if err != nil || got.OwnerID != "" || got.Status != StatusPublished || got.Builtin {
		t.Fatalf("legacy create = %+v, %v", got, err)
	}
	if err := s.VerifyEditToken("old", token); err != nil {
		t.Fatalf("legacy token = %v", err)
	}
	// The no-token legacy save cannot edit an ownerless row.
	if err := s.Save("old", toy{ID: "old", Value: 2}, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("legacy save of ownerless row = %v", err)
	}
	if err := s.Save("old", toy{ID: "old", Value: 2}, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("old"); got.Config.Value != 2 {
		t.Errorf("legacy admin save = %+v", got)
	}
	if err := s.Delete("old", false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("legacy delete without admin = %v", err)
	}
	if err := s.Delete("old", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("legacy delete = %v", err)
	}
	if err := s.Delete("missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("legacy delete missing = %v", err)
	}
}

func TestPGOwnerHelpersRequirePostgres(t *testing.T) {
	s := openToy(t, t.TempDir())
	if err := s.DeleteDraftsByOwner("o"); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("DeleteDraftsByOwner file mode = %v", err)
	}
	if err := s.OrphanPublishedByOwner("o"); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("OrphanPublishedByOwner file mode = %v", err)
	}
	if _, err := s.CreateDraft("x", toy{ID: "x", Value: 1}, "o"); !errors.Is(err, ErrNoDatabase) {
		t.Errorf("CreateDraft file mode = %v", err)
	}
}

// ---- helpers --------------------------------------------------------------

func pgtestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pgtest.Pool(t)
}

func publishValid(t *testing.T, s *Store[toy], name, owner string) error {
	t.Helper()
	if _, err := s.CreateDraft(name, toy{ID: name, Value: 1}, owner); err != nil {
		return err
	}
	_, err := s.Publish(name, Actor{AccountID: owner})
	return err
}

func mapNamesOf(list []Map[toy]) []string {
	names := make([]string, len(list))
	for i, m := range list {
		names[i] = m.Name
	}
	return names
}

func mustList(t *testing.T, fn func() ([]Map[toy], error)) []Map[toy] {
	t.Helper()
	list, err := fn()
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestPGImportLegacyInsertSkipReplace(t *testing.T) {
	pool := pgtestPool(t)
	builtin := t.TempDir()
	writeBuiltin(t, builtin, "alpha", toy{ID: "alpha", Value: 1})
	s := openPG(t, pool, builtin, nil)
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	if ok, err := s.Has("legacy"); err != nil || ok {
		t.Fatalf("Has before import = %v, %v", ok, err)
	}
	created, err := s.ImportLegacy("legacy", toy{ID: "legacy", Value: 5}, "", StatusPublished, hash)
	if err != nil || !created {
		t.Fatalf("ImportLegacy = %v, %v", created, err)
	}
	got, err := s.Get("legacy")
	if err != nil || got.OwnerID != "" || got.Status != StatusPublished || got.Config.Value != 5 {
		t.Fatalf("imported row = %+v, %v", got, err)
	}
	var stored string
	if err := pool.QueryRow(t.Context(),
		`SELECT edit_token_hash FROM platform_maps WHERE game = 'toy' AND id = 'legacy'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hash {
		t.Fatalf("stored token hash = %q, want %q", stored, hash)
	}
	if ok, err := s.Has("legacy"); err != nil || !ok {
		t.Fatalf("Has after import = %v, %v", ok, err)
	}

	// A second import never overwrites.
	created, err = s.ImportLegacy("legacy", toy{ID: "legacy", Value: 9}, "someone", StatusDraft, "")
	if err != nil || created {
		t.Fatalf("second ImportLegacy = %v, %v", created, err)
	}
	if got, _ := s.Get("legacy"); got.Config.Value != 5 || got.Status != StatusPublished {
		t.Fatalf("skip changed the row: %+v", got)
	}

	// Replace updates config, owner, status and token.
	if err := s.ReplaceLegacy("legacy", toy{ID: "legacy", Value: 7}, "owner", StatusDraft, ""); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get("legacy")
	if err != nil || got.OwnerID != "owner" || got.Status != StatusDraft || got.Config.Value != 7 {
		t.Fatalf("replaced row = %+v, %v", got, err)
	}
	if err := s.ReplaceLegacy("missing", toy{ID: "missing", Value: 1}, "", StatusPublished, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replace missing = %v", err)
	}

	// Plaintext or malformed token hashes never reach the table.
	if _, err := s.ImportLegacy("plain", toy{ID: "plain", Value: 1}, "", StatusPublished, "not-a-hash"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("plaintext token = %v", err)
	}
	if _, err := s.Get("plain"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected import left a row: %v", err)
	}

	// A builtin id is refused.
	if _, err := s.ImportLegacy("alpha", toy{ID: "alpha", Value: 2}, "", StatusPublished, ""); !errors.Is(err, ErrExists) {
		t.Fatalf("import over builtin = %v", err)
	}
}

func TestPGImportLegacyRequiresPostgres(t *testing.T) {
	s := openToy(t, t.TempDir())
	if _, err := s.ImportLegacy("x", toy{ID: "x", Value: 1}, "", StatusPublished, ""); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("file-mode import = %v", err)
	}
	if err := s.ReplaceLegacy("x", toy{ID: "x", Value: 1}, "", StatusPublished, ""); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("file-mode replace = %v", err)
	}
}

func TestPGPublishedMapsAlwaysPassPublishValidation(t *testing.T) {
	publish := func(v toy) error {
		if v.Value < 10 {
			return errors.New("not publishable")
		}
		return nil
	}
	pool := pgtestPool(t)
	s := openPG(t, pool, t.TempDir(), publish)
	owner := Actor{AccountID: "owner-a"}

	// A published row whose config fails the publish validator must reject an
	// admin edit that would leave it PUBLISHED, and must stay unchanged.
	if _, err := s.CreateDraft("pub", toy{ID: "pub", Value: 11}, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("pub", owner); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SaveOwned("pub", toy{ID: "pub", Value: 2}, Actor{Admin: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("admin published save = %+v, %v", got, err)
	}
	if got, _ := s.Get("pub"); got.Config.Value != 11 || got.Status != StatusPublished {
		t.Fatalf("rejected admin save changed the row: %+v", got)
	}

	// An admin edit of a DRAFT keeps it DRAFT and only needs structural validity.
	if _, err := s.CreateDraft("draft", toy{ID: "draft", Value: 1}, owner.AccountID); err != nil {
		t.Fatal(err)
	}
	got, err := s.SaveOwned("draft", toy{ID: "draft", Value: 2}, Actor{Admin: true})
	if err != nil || got.Status != StatusDraft {
		t.Fatalf("admin draft save = %+v, %v", got, err)
	}

	// Republishing a valid published map is idempotent.
	if _, err := s.Publish("pub", owner); err != nil {
		t.Fatalf("valid republish = %v", err)
	}
	// A published row made invalid behind the store's back is refused by Publish.
	if _, err := pool.Exec(t.Context(), `
UPDATE platform_maps SET config = '{"id":"pub","value":2}'::jsonb
WHERE game = 'toy' AND id = 'pub'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("pub", owner); !errors.Is(err, ErrInvalid) {
		t.Fatalf("publish invalid published row = %v", err)
	}

	// Builtins are PUBLISHED: an admin save that fails publish validation is
	// refused and leaves the file alone.
	builtin := t.TempDir()
	writeBuiltin(t, builtin, "alpha", toy{ID: "alpha", Value: 11})
	s2 := openPG(t, pool, builtin, publish)
	if err := s2.SaveBuiltin("alpha", toy{ID: "alpha", Value: 2}, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("builtin save = %v", err)
	}
	if got, _ := s2.Get("alpha"); got.Config.Value != 11 {
		t.Fatalf("rejected builtin save changed the file: %+v", got)
	}

	// Importing a PUBLISHED row must satisfy the publish gate; DRAFT imports
	// only need structural validity.
	if _, err := s.ImportLegacy("imported", toy{ID: "imported", Value: 2}, "", StatusPublished, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("import invalid published = %v", err)
	}
	if _, err := s.Get("imported"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected import left a row: %v", err)
	}
	if created, err := s.ImportLegacy("imported_draft", toy{ID: "imported_draft", Value: 2}, "o", StatusDraft, ""); err != nil || !created {
		t.Fatalf("import draft = %v, %v", created, err)
	}
}
