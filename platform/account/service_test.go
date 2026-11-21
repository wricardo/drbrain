package account

import (
	"errors"
	"sync"
	"testing"
)

// fakeRepo is a minimal in-memory Repository for service tests.
type fakeRepo struct {
	mu       sync.Mutex
	accounts map[string]Account
	deleted  []string
}

func newFakeRepo(accounts ...Account) *fakeRepo {
	r := &fakeRepo{accounts: make(map[string]Account)}
	for _, a := range accounts {
		r.accounts[a.ID] = a
	}
	return r
}

func (r *fakeRepo) Create(a Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[a.ID] = a
	return nil
}

func (r *fakeRepo) Get(id string) (Account, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[id]
	return a, ok
}

func (r *fakeRepo) ByIdentity(provider, providerUserID string) (Account, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.accounts {
		if a.Provider == provider && a.ProviderUserID == providerUserID {
			return a, true
		}
	}
	return Account{}, false
}

func (r *fakeRepo) ByHandle(handle string) (Account, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.accounts {
		if a.Handle == handle {
			return a, true
		}
	}
	return Account{}, false
}

func (r *fakeRepo) Update(id string, fn func(*Account) error) (Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[id]
	if !ok {
		return Account{}, errors.New("not found")
	}
	if err := fn(&a); err != nil {
		return Account{}, err
	}
	r.accounts[id] = a
	return a, nil
}

func (r *fakeRepo) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.accounts, id)
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *fakeRepo) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.accounts)
}

func (r *fakeRepo) wasDeleted(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.deleted {
		if got == id {
			return true
		}
	}
	return false
}

func hookRecorder() (calls *[]string, hook func(string) error) {
	calls = &[]string{}
	return calls, func(id string) error {
		*calls = append(*calls, id)
		return nil
	}
}

func TestDeleteAccountWithoutHooks(t *testing.T) {
	repo := newFakeRepo(acct(id1, "otter", "google", "u1"))
	s := New(Options{Store: repo})
	if err := s.DeleteAccount(id1); err != nil {
		t.Fatal(err)
	}
	if !repo.wasDeleted(id1) {
		t.Error("account was not deleted")
	}
}

func TestDeleteAccountRunsEveryHookInOrder(t *testing.T) {
	repo := newFakeRepo(acct(id1, "otter", "google", "u1"))
	calls1, hook1 := hookRecorder()
	calls2, hook2 := hookRecorder()
	s := New(Options{Store: repo, OnDelete: hook1})
	s.AddOnDelete(hook2)
	if err := s.DeleteAccount(id1); err != nil {
		t.Fatal(err)
	}
	if len(*calls1) != 1 || (*calls1)[0] != id1 {
		t.Errorf("first hook calls = %v", *calls1)
	}
	if len(*calls2) != 1 || (*calls2)[0] != id1 {
		t.Errorf("second hook calls = %v", *calls2)
	}
	if !repo.wasDeleted(id1) {
		t.Error("account was not deleted")
	}
}

func TestSetOnDeleteReplacesHooks(t *testing.T) {
	repo := newFakeRepo(acct(id1, "otter", "google", "u1"))
	original, originalHook := hookRecorder()
	replacement, replacementHook := hookRecorder()
	s := New(Options{Store: repo, OnDelete: originalHook})
	s.AddOnDelete(func(string) error { t.Error("replaced hook ran"); return nil })
	s.SetOnDelete(replacementHook)
	if err := s.DeleteAccount(id1); err != nil {
		t.Fatal(err)
	}
	if len(*original) != 0 {
		t.Errorf("original hook ran: %v", *original)
	}
	if len(*replacement) != 1 {
		t.Errorf("replacement hook did not run: %v", *replacement)
	}

	// nil clears every hook.
	repo = newFakeRepo(acct(id1, "otter", "google", "u1"))
	s = New(Options{Store: repo, OnDelete: originalHook})
	s.SetOnDelete(nil)
	if err := s.DeleteAccount(id1); err != nil {
		t.Fatal(err)
	}
	if len(*original) != 0 {
		t.Errorf("cleared hook ran: %v", *original)
	}
	if !repo.wasDeleted(id1) {
		t.Error("account was not deleted")
	}
}

func TestDeleteAccountHookFailureAborts(t *testing.T) {
	repo := newFakeRepo(acct(id1, "otter", "google", "u1"))
	boom := errors.New("detach failed")
	late, lateHook := hookRecorder()
	s := New(Options{Store: repo, OnDelete: func(string) error { return boom }})
	s.AddOnDelete(func(string) error { return errors.New("second failure") })
	s.AddOnDelete(lateHook)
	err := s.DeleteAccount(id1)
	if err == nil {
		t.Fatal("DeleteAccount succeeded despite hook failures")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error does not include the first failure: %v", err)
	}
	if err.Error() == "" {
		t.Error("empty error")
	}
	if len(*late) != 1 {
		t.Errorf("later hook did not run: %v", *late)
	}
	if repo.wasDeleted(id1) {
		t.Error("account was deleted despite hook failure")
	}
	if _, ok := repo.Get(id1); !ok {
		t.Error("account row disappeared")
	}
}

func TestDeleteAccountJoinIncludesEveryFailure(t *testing.T) {
	repo := newFakeRepo(acct(id1, "otter", "google", "u1"))
	first := errors.New("first")
	second := errors.New("second")
	s := New(Options{Store: repo})
	s.AddOnDelete(func(string) error { return first })
	s.AddOnDelete(func(string) error { return second })
	err := s.DeleteAccount(id1)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Errorf("joined error = %v, want both failures", err)
	}
}
