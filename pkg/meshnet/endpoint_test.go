package meshnet

import "testing"

func TestListenPortFromEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     int
		wantErr  bool
	}{
		{name: "valid", endpoint: "203.0.113.5:51820", want: 51820},
		{name: "valid hostname", endpoint: "mesh.example.com:12345", want: 12345},
		{name: "missing port", endpoint: "203.0.113.5", wantErr: true},
		{name: "empty", endpoint: "", wantErr: true},
		{name: "non-numeric port", endpoint: "203.0.113.5:mesh", wantErr: true},
		{name: "port out of range", endpoint: "203.0.113.5:70000", wantErr: true},
		{name: "port zero", endpoint: "203.0.113.5:0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ListenPortFromEndpoint(tt.endpoint)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ListenPortFromEndpoint(%q) = %d, want an error", tt.endpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListenPortFromEndpoint(%q): %v", tt.endpoint, err)
			}
			if got != tt.want {
				t.Fatalf("ListenPortFromEndpoint(%q) = %d, want %d", tt.endpoint, got, tt.want)
			}
		})
	}
}
