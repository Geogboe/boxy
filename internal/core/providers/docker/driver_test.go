package docker

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/v2/internal/core/model"
)

func TestDriver_ValidateConfig(t *testing.T) {
	d := New()

	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr bool
	}{
		{name: "empty", cfg: map[string]any{}, wantErr: true},
		{name: "host", cfg: map[string]any{"host": "unix:///var/run/docker.sock"}, wantErr: false},
		{name: "context", cfg: map[string]any{"context": "default"}, wantErr: false},
		{name: "both host and context", cfg: map[string]any{"host": "unix:///var/run/docker.sock", "context": "default"}, wantErr: true},
		{name: "bad host scheme", cfg: map[string]any{"host": "http://example"}, wantErr: true},
		{name: "tls requires tcp host", cfg: map[string]any{"host": "unix:///var/run/docker.sock", "tls": map[string]any{"enabled": true}}, wantErr: true},
		{name: "tls with tcp host", cfg: map[string]any{"host": "tcp://docker:2376", "tls": map[string]any{"enabled": true}}, wantErr: false},
	}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
			p := model.Provider{Name: "p", Type: "docker", Config: tt.cfg}
			err := d.ValidateConfig(context.Background(), p)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
