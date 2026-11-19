package account

import (
	"sort"

	"github.com/wricardo/drbrain-motor-programming/game/service"
)

// Accounts returns a copy of every account the store holds, oldest first
// (password hashes included). It is for the JSON import and export tool.
func (s *Store) Accounts() []service.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]service.Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Logins returns a copy of every login the store holds, oldest first.
func (s *Store) Logins() []service.Login {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]service.Login, 0, len(s.logins))
	for _, l := range s.logins {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].TokenHash < out[j].TokenHash
	})
	return out
}
