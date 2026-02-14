package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

// newSandboxID generates a random sandbox ID.
//
// This avoids external dependencies and is sufficient for scaffolding/runtime.
func newSandboxID() (model.SandboxID, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return model.SandboxID("sbx_" + hex.EncodeToString(b)), nil
}
