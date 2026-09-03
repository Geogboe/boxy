package docker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestDriverCreateRejectsRawSourceDescriptor(t *testing.T) {
	t.Parallel()
	_, err := (&Driver{}).Create(context.Background(), &CreateConfig{
		Source: &providersdk.SourceDescriptor{Format: "vhdx", ExpiresAt: time.Now().Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support raw source format") {
		t.Fatalf("error = %v", err)
	}
}
