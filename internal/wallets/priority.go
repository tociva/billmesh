package wallets

import "time"

type GrantCandidate struct {
	ID, Source string
	Remaining  int64
	ExpiresAt  *time.Time
	CreatedAt  time.Time
}

func Prioritize(grants []GrantCandidate) []GrantCandidate {
	result := append([]GrantCandidate(nil), grants...)
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && lessGrant(result[j], result[j-1]); j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}
func lessGrant(a, b GrantCandidate) bool {
	if a.ExpiresAt != nil && b.ExpiresAt == nil {
		return true
	}
	if a.ExpiresAt == nil && b.ExpiresAt != nil {
		return false
	}
	if a.ExpiresAt != nil && !a.ExpiresAt.Equal(*b.ExpiresAt) {
		return a.ExpiresAt.Before(*b.ExpiresAt)
	}
	if a.Source != b.Source {
		return a.Source == "subscription"
	}
	return a.CreatedAt.Before(b.CreatedAt)
}
