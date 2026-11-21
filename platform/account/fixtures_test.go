package account

import "time"

const (
	id1 = "0123456789abcdef01234567"
	id2 = "fedcba9876543210fedcba98"
)

func acct(id, handle, provider, user string) Account {
	return Account{
		ID: id, Provider: provider, ProviderUserID: user, Handle: handle, DisplayName: handle,
		ProfilePublic: true, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}
