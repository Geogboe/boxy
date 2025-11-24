package runtime

import (
	"fmt"

	"github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/storage"
)

// OpenStorage opens the configured storage (sqlite only today).
func OpenStorage(cfg *config.Config) (storage.Store, error) {
	if cfg.Storage.Type != "sqlite" {
		return nil, fmt.Errorf("unsupported storage type: %s", cfg.Storage.Type)
	}
	return storage.NewSQLiteStore(cfg.Storage.Path)
}
