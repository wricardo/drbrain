package mapstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type toy struct {
	ID    string `json:"id"`
	Value int    `json:"value"`
}

func decodeToy(raw []byte) (toy, error) {
	var t toy
	err := json.Unmarshal(raw, &t)
	return t, err
}

func encodeToy(t toy) ([]byte, error) { return json.Marshal(t) }

func validateToy(t toy) error {
	if t.ID == "" {
		return errors.New("id required")
	}
	if t.Value <= 0 {
		return errors.New("value must be positive")
	}
	return nil
}

func openToy(t *testing.T, builtin string) *Store[toy] {
	t.Helper()
	s, err := Open(Options[toy]{
		BuiltinDir: builtin,
		CustomDir:  filepath.Join(builtin, "..", "custom"),
		Decode:     decodeToy,
		Encode:     encodeToy,
		Validate:   validateToy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeBuiltin(t *testing.T, dir, name string, cfg toy) {
	t.Helper()
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListAndGet(t *testing.T) {
	builtin := t.TempDir()
	writeBuiltin(t, builtin, "alpha", toy{ID: "alpha", Value: 1})
	writeBuiltin(t, builtin, "beta", toy{ID: "beta", Value: 2})
	s := openToy(t, builtin)

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Builtin || list[0].Name != "alpha" {
		t.Fatalf("List = %+v", list)
	}
	got, err := s.Get("alpha.json")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Builtin || got.Config.ID != "alpha" {
		t.Errorf("Get = %+v", got)
	}
	if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing err = %v", err)
	}
}

func TestCreateTokenAndDuplicate(t *testing.T) {
	builtin := t.TempDir()
	writeBuiltin(t, builtin, "alpha", toy{ID: "alpha", Value: 1})
	s := openToy(t, builtin)

	token, err := s.Create("mine", toy{ID: "mine", Value: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEditToken("mine", token); err != nil {
		t.Fatalf("VerifyEditToken: %v", err)
	}
	if err := s.VerifyEditToken("mine", "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("wrong token err = %v", err)
	}
	if err := s.VerifyEditToken("alpha", token); !errors.Is(err, ErrReadOnly) {
		t.Errorf("builtin token err = %v", err)
	}

	// No partial file on a refused duplicate.
	if _, err := s.Create("mine", toy{ID: "mine", Value: 4}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate err = %v", err)
	}
	if _, err := s.Create("alpha", toy{ID: "alpha", Value: 4}); !errors.Is(err, ErrExists) {
		t.Errorf("builtin duplicate err = %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.CustomDir(), "mine.json"))
	var got toy
	_ = json.Unmarshal(raw, &got)
	if got.Value != 3 {
		t.Errorf("duplicate create rewrote the map: %+v", got)
	}
}

func TestBuiltinAdminGate(t *testing.T) {
	builtin := t.TempDir()
	writeBuiltin(t, builtin, "alpha", toy{ID: "alpha", Value: 1})
	s := openToy(t, builtin)

	if err := s.Save("alpha", toy{ID: "alpha", Value: 2}, false); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Save without admin err = %v", err)
	}
	if err := s.SaveBuiltin("alpha", toy{ID: "alpha", Value: 2}, false); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("SaveBuiltin without admin err = %v", err)
	}
	if err := s.SaveBuiltin("alpha", toy{ID: "alpha", Value: 2}, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("alpha")
	if got.Config.Value != 2 {
		t.Errorf("admin save not visible: %+v", got)
	}

	if err := s.Delete("alpha", false); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Delete without admin err = %v", err)
	}
	if err := s.DeleteBuiltin("alpha", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted builtin still present: %v", err)
	}
}

func TestCustomSaveDeleteAndLegacyToken(t *testing.T) {
	builtin := t.TempDir()
	s := openToy(t, builtin)

	// A custom map written by the old code, with its old token sidecar.
	raw, _ := json.Marshal(toy{ID: "old", Value: 7})
	if err := os.WriteFile(filepath.Join(s.CustomDir(), "old.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("legacy-token"))
	rec, _ := json.Marshal(map[string]string{"token_hash": hex.EncodeToString(sum[:])})
	if err := os.WriteFile(filepath.Join(s.CustomDir(), "old.json.edit-token"), rec, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEditToken("old", "legacy-token"); err != nil {
		t.Fatalf("legacy token rejected: %v", err)
	}
	if err := s.Save("old", toy{ID: "old", Value: 8}, false); err != nil {
		t.Fatalf("custom save: %v", err)
	}
	got, _ := s.Get("old")
	if got.Config.Value != 8 || got.Builtin {
		t.Errorf("saved custom = %+v", got)
	}
	if err := s.Delete("old", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.CustomDir(), "old.json")); !os.IsNotExist(err) {
		t.Error("custom map file survived delete")
	}
}

func TestValidationRefusesWrite(t *testing.T) {
	s := openToy(t, t.TempDir())
	if _, err := s.Create("bad", toy{ID: "bad", Value: 0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid create err = %v", err)
	}
	entries, _ := os.ReadDir(s.CustomDir())
	if len(entries) != 0 {
		t.Errorf("invalid create left files: %v", entries)
	}
}

// TestConcurrentAccess exercises the store's locks under -race.
func TestConcurrentAccess(t *testing.T) {
	s := openToy(t, t.TempDir())
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("m%02d", i)
			if _, err := s.Create(name, toy{ID: name, Value: i + 1}); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.Get(name); err != nil {
				t.Error(err)
			}
			if _, err := s.List(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 50 {
		t.Errorf("List = %d, want 50", len(list))
	}
}
