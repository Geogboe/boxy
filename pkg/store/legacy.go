package store

import (
	"context"

	"github.com/Geogboe/boxy/pkg/model"
)

// LegacyPoolGuestCredentialStore is the migration-only surface for plaintext
// credentials embedded in the historical state.json format.
type LegacyPoolGuestCredentialStore interface {
	ListPoolGuestCredentials(context.Context) (map[model.PoolName]string, error)
	DeletePoolGuestCredential(context.Context, model.PoolName) error
}
