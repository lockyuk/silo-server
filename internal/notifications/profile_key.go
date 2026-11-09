package notifications

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/access"
)

// ScopeResolver computes the effective library visibility for a
// (user, profile) outside of a request context. Satisfied by
// *access.Resolver.
type ScopeResolver interface {
	Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error)
}

// profileKey names one profile on one account. A profile id alone is not
// unique: every account from before profiles has a profile named "default".
type profileKey struct {
	userID    int
	profileID string
}

// String renders the key as account/profile for logs and errors.
func (k profileKey) String() string { return strconv.Itoa(k.userID) + "/" + k.profileID }

// LogValue logs the key as account/profile in every slog handler; the JSON
// handler would otherwise encode the unexported fields as {}.
func (k profileKey) LogValue() slog.Value { return slog.StringValue(k.String()) }

// profileKeyArrays splits keys into the parallel account and profile arrays a
// query matches with (user_id, profile_id) IN (SELECT * FROM unnest($1::int[], $2::text[])).
func profileKeyArrays(keys []profileKey) ([]int, []string) {
	userIDs := make([]int, len(keys))
	profileIDs := make([]string, len(keys))
	for i, key := range keys {
		userIDs[i], profileIDs[i] = key.userID, key.profileID
	}
	return userIDs, profileIDs
}
