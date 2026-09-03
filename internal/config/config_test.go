package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
)

func TestServerSpec_AgentTransportFields(t *testing.T) {
	t.Parallel()

	t.Run("round_trip_yaml", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "boxy.yaml")
		if err := os.WriteFile(p, []byte(`
server:
  listen: ":9090"
  grpc_listen: ":9095"
  agent_heartbeat_interval: 30s
  grpc_cert_sans:
    - agent.example.test
    - 192.0.2.5
`), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		cfg, err := LoadFile(p)
		if err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
		if cfg.Server.GRPCListen != ":9095" {
			t.Fatalf("GRPCListen = %q, want :9095", cfg.Server.GRPCListen)
		}
		d, err := cfg.Server.EffectiveAgentHeartbeatInterval()
		if err != nil {
			t.Fatalf("EffectiveAgentHeartbeatInterval: %v", err)
		}
		if d != 30*time.Second {
			t.Fatalf("interval = %v, want 30s", d)
		}
		wantSANs := []string{"agent.example.test", "192.0.2.5"}
		if !slices.Equal(cfg.Server.GRPCCertSANs, wantSANs) {
			t.Fatalf("GRPCCertSANs = %v, want %v", cfg.Server.GRPCCertSANs, wantSANs)
		}
	})

	t.Run("unset_interval_defaults", func(t *testing.T) {
		d, err := ServerSpec{}.EffectiveAgentHeartbeatInterval()
		if err != nil {
			t.Fatalf("EffectiveAgentHeartbeatInterval: %v", err)
		}
		if d != DefaultAgentHeartbeatInterval {
			t.Fatalf("interval = %v, want default %v", d, DefaultAgentHeartbeatInterval)
		}
	})

	t.Run("diagnostics_retention_defaults_to_fourteen_days", func(t *testing.T) {
		d, err := ServerSpec{}.EffectiveDiagnosticsRetention()
		if err != nil {
			t.Fatalf("EffectiveDiagnosticsRetention: %v", err)
		}
		if d != DefaultDiagnosticsRetention {
			t.Fatalf("retention = %v, want default %v", d, DefaultDiagnosticsRetention)
		}
	})

	t.Run("diagnostics_retention_parses_duration", func(t *testing.T) {
		d, err := (ServerSpec{DiagnosticsRetention: "48h"}).EffectiveDiagnosticsRetention()
		if err != nil || d != 48*time.Hour {
			t.Fatalf("retention = %v, err = %v, want 48h", d, err)
		}
	})

	t.Run("invalid_diagnostics_retention_fails_validate", func(t *testing.T) {
		cfg := Config{Server: ServerSpec{DiagnosticsRetention: "not-a-duration"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate: expected error for an unparseable diagnostics retention")
		}
	})

	t.Run("invalid_interval_fails_validate", func(t *testing.T) {
		cfg := Config{Server: ServerSpec{AgentHeartbeatInterval: "not-a-duration"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate: expected error for an unparseable heartbeat interval")
		}
	})

	t.Run("negative_interval_fails_validate", func(t *testing.T) {
		cfg := Config{Server: ServerSpec{AgentHeartbeatInterval: "-5s"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate: expected error for a negative heartbeat interval")
		}
	})

	t.Run("empty_grpc_cert_san_fails_validate", func(t *testing.T) {
		cfg := Config{Server: ServerSpec{GRPCCertSANs: []string{"valid.example.test", "  "}}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate: expected error for a blank grpc_cert_sans entry")
		}
	})

	t.Run("valid_grpc_cert_sans_pass_validate", func(t *testing.T) {
		cfg := Config{Server: ServerSpec{GRPCCertSANs: []string{"agent.example.test", "192.0.2.5"}}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: unexpected error for valid grpc_cert_sans: %v", err)
		}
	})

	t.Run("unknown_server_field_rejected", func(t *testing.T) {
		// Regression guard: KnownFields(true) must reject unknown keys in
		// nested structs like ServerSpec, not just at the top level — this
		// is what lets new fields skip a hand-written whitelist unmarshaler
		// (unlike PoolSpec, which has one).
		dir := t.TempDir()
		p := filepath.Join(dir, "boxy.yaml")
		if err := os.WriteFile(p, []byte(`
server:
  listen: ":9090"
  insecure: true
`), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := LoadFile(p); err == nil {
			t.Fatal("LoadFile: expected an unknown-field error for server.insecure (deliberately flag-only, never config)")
		}
	})
}

func TestSecretSpec_RequiresExplicitValidBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    SecretSpec
		wantErr bool
	}{
		{name: "unset", spec: SecretSpec{}},
		{name: "file", spec: SecretSpec{Backend: string(boxysecrets.BackendFile), Path: "secrets.json"}},
		{name: "keyring", spec: SecretSpec{Backend: string(boxysecrets.BackendKeyring)}},
		{name: "dpapi", spec: SecretSpec{Backend: string(boxysecrets.BackendDPAPI), Path: "secrets.json"}},
		{name: "missing backend", spec: SecretSpec{Path: "secrets.json"}, wantErr: true},
		{name: "file missing path", spec: SecretSpec{Backend: string(boxysecrets.BackendFile)}, wantErr: true},
		{name: "keyring path", spec: SecretSpec{Backend: string(boxysecrets.BackendKeyring), Path: "secrets.json"}, wantErr: true},
		{name: "unknown", spec: SecretSpec{Backend: "vault"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}

	if err := (Config{Server: ServerSpec{Secrets: SecretSpec{Backend: string(boxysecrets.BackendFile), Path: "secrets.json"}}}).Validate(); err != nil {
		t.Fatalf("Config.Validate() = %v, want nil", err)
	}
}

func TestOIDCSpec_Validate(t *testing.T) {
	t.Parallel()

	valid := OIDCSpec{
		Issuer:       "https://idp.example.invalid/realms/boxy",
		ClientID:     "boxy",
		ClientSecret: "env:BOXY_TEST_OIDC_CLIENT_SECRET",
		RedirectURL:  "https://boxy.example.invalid/auth/callback",
		RoleClaim:    "groups",
		RoleMapping:  map[string]string{"boxy-admins": "admin"},
	}

	tests := []struct {
		name    string
		mutate  func(o OIDCSpec) OIDCSpec
		wantErr bool
	}{
		{name: "unset", mutate: func(OIDCSpec) OIDCSpec { return OIDCSpec{} }},
		{name: "valid", mutate: func(o OIDCSpec) OIDCSpec { return o }},
		{name: "missing client id", mutate: func(o OIDCSpec) OIDCSpec { o.ClientID = ""; return o }, wantErr: true},
		{name: "literal client secret", mutate: func(o OIDCSpec) OIDCSpec { o.ClientSecret = "not-an-env-ref"; return o }, wantErr: true},
		{name: "missing client secret", mutate: func(o OIDCSpec) OIDCSpec { o.ClientSecret = ""; return o }, wantErr: true},
		{name: "missing redirect url", mutate: func(o OIDCSpec) OIDCSpec { o.RedirectURL = ""; return o }, wantErr: true},
		{name: "missing role claim", mutate: func(o OIDCSpec) OIDCSpec { o.RoleClaim = ""; return o }, wantErr: true},
		{name: "empty role mapping", mutate: func(o OIDCSpec) OIDCSpec { o.RoleMapping = nil; return o }, wantErr: true},
		{name: "invalid role mapping value", mutate: func(o OIDCSpec) OIDCSpec {
			o.RoleMapping = map[string]string{"x": "superuser"}
			return o
		}, wantErr: true},
		{name: "invalid default role", mutate: func(o OIDCSpec) OIDCSpec { o.DefaultRole = "superuser"; return o }, wantErr: true},
		{name: "valid default role", mutate: func(o OIDCSpec) OIDCSpec { o.DefaultRole = "user"; return o }},
		{name: "invalid session ttl", mutate: func(o OIDCSpec) OIDCSpec { o.SessionTTL = "not-a-duration"; return o }, wantErr: true},
		{name: "non-positive session ttl", mutate: func(o OIDCSpec) OIDCSpec { o.SessionTTL = "0h"; return o }, wantErr: true},
		{name: "valid session ttl", mutate: func(o OIDCSpec) OIDCSpec { o.SessionTTL = "6h"; return o }},
		{name: "local login hidden without oidc", mutate: func(o OIDCSpec) OIDCSpec { o.Issuer = ""; o.HideLocalLogin = true; return o }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mutate(valid).Validate()
			if tt.wantErr && err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}

	if err := (Config{Server: ServerSpec{OIDC: valid}}).Validate(); err != nil {
		t.Fatalf("Config.Validate() = %v, want nil", err)
	}
}

func TestLoadFile_YAML_HappyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
providers:
  - name: local
    type: docker
    config:
      host: unix:///var/run/docker.sock
pools: []
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "local" {
		t.Fatalf("provider name = %q, want %q", cfg.Providers[0].Name, "local")
	}
	if cfg.Providers[0].Type != "docker" {
		t.Fatalf("provider type = %q, want %q", cfg.Providers[0].Type, "docker")
	}
}

func TestLoadFile_YAML_AcceptsPoolsBlob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
providers: []
pools:
  - name: kali-attackers
    type: container
    provider: docker-local
    config:
      image: kalilinux/kali-rolling
    policy:
      preheat:
        min_ready: 3
        max_total: 8
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("providers len = %d, want 0", len(cfg.Providers))
	}
	if len(cfg.Pools) != 1 {
		t.Fatalf("pools len = %d, want 1", len(cfg.Pools))
	}
}

func TestLoadFile_JSON_HappyPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.json")
	if err := os.WriteFile(p, []byte(`{
  "providers": [
    {
      "name": "local",
      "type": "docker",
      "config": {
        "host": "unix:///var/run/docker.sock"
      }
    }
  ],
  "pools": []
}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.Providers))
	}
}

func TestLoadFile_YAML_UnknownTopLevelFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
extra: 1
providers: []
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestLoadFile_JSON_UnknownTopLevelFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.json")
	if err := os.WriteFile(p, []byte(`{"extra":1,"providers":[]}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestLoadFile_YAML_UnknownProviderFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(p, []byte(`
providers:
  - name: local
    type: docker
    bogus: 1
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestServerSpec_UIEnabled(t *testing.T) {
	t.Parallel()

	t.Run("nil_defaults_true", func(t *testing.T) {
		s := ServerSpec{}
		if !s.UIEnabled() {
			t.Fatal("UIEnabled() = false, want true (nil default)")
		}
	})

	t.Run("explicit_true", func(t *testing.T) {
		v := true
		s := ServerSpec{UI: &v}
		if !s.UIEnabled() {
			t.Fatal("UIEnabled() = false, want true")
		}
	})

	t.Run("explicit_false", func(t *testing.T) {
		v := false
		s := ServerSpec{UI: &v}
		if s.UIEnabled() {
			t.Fatal("UIEnabled() = true, want false")
		}
	})
}

func TestLoadFile_JSON_UnknownProviderFieldFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "boxy.json")
	if err := os.WriteFile(p, []byte(`{
  "providers": [
    {
      "name": "local",
      "type": "docker",
      "bogus": 1
    }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := LoadFile(p); err == nil {
		t.Fatalf("LoadFile: expected error, got nil")
	}
}

func TestResolvePoolExpectedType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    model.ResourceType
		wantErr string
	}{
		{name: "empty defaults to container", input: "", want: model.ResourceTypeContainer},
		{name: "container", input: "container", want: model.ResourceTypeContainer},
		{name: "docker", input: "docker", want: model.ResourceTypeContainer},
		{name: "vm", input: "vm", want: model.ResourceTypeVM},
		{name: "share", input: "share", want: model.ResourceTypeShare},
		{name: "invalid", input: "badtype", want: model.ResourceTypeUnknown, wantErr: `unsupported pool type "badtype"`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolvePoolExpectedType(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolvePoolExpectedType(%q) error = nil, want %q", tt.input, tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("ResolvePoolExpectedType(%q) error = %q, want %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePoolExpectedType(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ResolvePoolExpectedType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigValidate_invalid_pool_type(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Pools: []PoolSpec{
			{Name: "test", Type: "badtype"},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want invalid pool type")
	}
	if got, want := err.Error(), `pool "test" type invalid: unsupported pool type "badtype"`; got != want {
		t.Fatalf("Validate() error = %q, want %q", got, want)
	}
}

func TestConfigValidate_valid_pool_type_aliases(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Pools: []PoolSpec{
			{Name: "default-empty"},
			{Name: "container", Type: "container"},
			{Name: "docker", Type: "docker"},
			{Name: "vm", Type: "vm"},
			{Name: "share", Type: "share"},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestPoolSpec_PreheatExplicitness(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(path, []byte(`
providers: []
pools:
  - name: omitted-max
    type: container
    policy:
      preheat:
        min_ready: 0
  - name: explicit-drain
    type: container
    policy:
      preheat:
        min_ready: 0
        max_total: 0
  - name: alias-drain
    type: container
    policies:
      preheat:
        max_total: 0
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	omitted := cfg.Pools[0].EffectivePolicy().Preheat
	if omitted.MaxTotal != 0 || omitted.MaxTotalSet() {
		t.Fatalf("omitted max_total = (%d, set=%t), want zero and unset", omitted.MaxTotal, omitted.MaxTotalSet())
	}
	if omitted.ConfiguresDrain() {
		t.Fatal("omitted max_total configured drain")
	}

	explicit := cfg.Pools[1].EffectivePolicy().Preheat
	if explicit.MaxTotal != 0 || !explicit.MaxTotalSet() || !explicit.ConfiguresDrain() {
		t.Fatalf("explicit max_total = (%d, set=%t, drain=%t), want explicit drain", explicit.MaxTotal, explicit.MaxTotalSet(), explicit.ConfiguresDrain())
	}

	alias := cfg.Pools[2]
	if !alias.PoliciesSet() || alias.PolicySet() {
		t.Fatalf("alias flags policy=%t policies=%t, want only policies", alias.PolicySet(), alias.PoliciesSet())
	}
	if !alias.EffectivePolicy().Preheat.ConfiguresDrain() {
		t.Fatal("policies alias explicit max_total: 0 did not configure drain")
	}
}

func TestConfigValidate_rejectsPolicyAliasesTogether(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(path, []byte(`
providers: []
pools:
  - name: web
    type: container
    policy:
      preheat:
        min_ready: 0
    policies:
      preheat:
        max_total: 0
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	err = cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want policy alias conflict")
	}
	if got, want := err.Error(), `pool "web" sets both policy and policies; use only one`; got != want {
		t.Fatalf("Validate() error = %q, want %q", got, want)
	}
}

func TestConfigValidate_rejectsDrainWithPositiveMinReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "boxy.yaml")
	if err := os.WriteFile(path, []byte(`
providers: []
pools:
  - name: web
    type: container
    policy:
      preheat:
        min_ready: 1
        max_total: 0
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	err = cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want drain/min_ready conflict")
	}
	if got, want := err.Error(), `pool "web" preheat max_total: 0 conflicts with min_ready: 1`; got != want {
		t.Fatalf("Validate() error = %q, want %q", got, want)
	}
}

func TestConfigValidate_rejectsInvalidArtifactStoreReferences(t *testing.T) {
	t.Parallel()

	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := map[string]struct {
		stores  map[string]ArtifactStoreSpec
		wantErr string
	}{
		"unknown store": {
			stores:  map[string]ArtifactStoreSpec{"images": {Type: "local", Path: t.TempDir()}},
			wantErr: `source "windows-2022" references unknown artifact store "missing"`,
		},
		"unsupported type": {
			stores:  map[string]ArtifactStoreSpec{"images": {Type: "ftp"}},
			wantErr: `artifact store "images" has unsupported type "ftp"`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{
				ArtifactStores: tt.stores,
				Sources: map[string]SourceSpec{
					"windows-2022": {Store: "missing", Path: "images/windows.vhdx", Digest: digest},
				},
			}
			if name == "unsupported type" {
				cfg.Sources["windows-2022"] = SourceSpec{Store: "images", Path: "images/windows.vhdx", Digest: digest}
			}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidate_rejectsInvalidSourceMetadata(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		source  SourceSpec
		wantErr string
	}{
		"missing store": {
			source:  SourceSpec{Path: "images/windows.vhdx", Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			wantErr: `source "windows-2022" store is required`,
		},
		"missing path": {
			source:  SourceSpec{Store: "images", Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			wantErr: `source "windows-2022" path is required`,
		},
		"invalid digest": {
			source:  SourceSpec{Store: "images", Path: "images/windows.vhdx", Digest: "sha1:bad"},
			wantErr: `source "windows-2022": digest must use sha256:<64 hex characters>`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := Config{
				ArtifactStores: map[string]ArtifactStoreSpec{"images": {Type: "local"}},
				Sources:        map[string]SourceSpec{"windows-2022": tt.source},
			}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidate_rejectsPromotionPackageWithoutPromotionEvent(t *testing.T) {
	cfg := Config{
		Packages: map[string]resourcepack.Manifest{
			"base": {
				Name:    "base",
				Version: "1.0.0",
				Method:  resourcepack.MethodShell,
				Scopes:  []resourcepack.Scope{resourcepack.ScopeResource},
				Events:  []resourcepack.Event{resourcepack.EventProvision},
			},
		},
		Templates: map[string]TemplateSpec{
			"base":    {Type: "container", Packages: []string{"base@1.0.0"}},
			"derived": {Extends: "base"},
		},
		Pools: []PoolSpec{{Name: "derived", Template: "derived"}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "promotion") {
		t.Fatalf("Validate() error = %v, want promotion event validation error", err)
	}
}

func TestConfigValidate_rejectsUnsupportedPackageMethod(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Packages: map[string]resourcepack.Manifest{
			"future-config": {
				Name:    "future-config",
				Version: "1.0.0",
				Method:  resourcepack.MethodDSC,
				Scopes:  []resourcepack.Scope{resourcepack.ScopeResource},
				Events:  []resourcepack.Event{resourcepack.EventProvision},
			},
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Validate() error = %v, want unsupported package method error", err)
	}
}

func TestConfigValidate_rejectsUnsupportedBuiltinPackageManager(t *testing.T) {
	t.Parallel()

	cfg := Config{Packages: map[string]resourcepack.Manifest{
		"tools": {
			Version: "1.0.0",
			Builtin: resourcepack.BuiltinPackageManager,
			Scopes:  []resourcepack.Scope{resourcepack.ScopeResource},
			Events:  []resourcepack.Event{resourcepack.EventProvision},
			Inputs: map[string]any{"parameters": map[string]any{
				"manager": "brew", "packages": []any{"curl"},
			}},
		},
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported package manager") {
		t.Fatalf("Config.Validate() error = %v, want unsupported package manager", err)
	}
}

func TestConfigValidateRejectsUnreferencedPackageGraphErrors(t *testing.T) {
	t.Parallel()

	base := func(name string, deps []string) resourcepack.Manifest {
		return resourcepack.Manifest{
			Name: name, Version: "1.0.0", Method: resourcepack.MethodShell,
			Scopes: []resourcepack.Scope{resourcepack.ScopeResource},
			Events: []resourcepack.Event{resourcepack.EventProvision}, Dependencies: deps,
		}
	}
	tests := []struct {
		name     string
		packages map[string]resourcepack.Manifest
		want     string
	}{
		{name: "missing", packages: map[string]resourcepack.Manifest{"unused": base("unused", []string{"missing@1.0.0"})}, want: "missing@1.0.0"},
		{name: "cycle", packages: map[string]resourcepack.Manifest{
			"a": base("a", []string{"b@1.0.0"}), "b": base("b", []string{"a@1.0.0"}),
		}, want: "a@1.0.0 -> b@1.0.0 -> a@1.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (Config{Packages: tt.packages}).Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigValidateRejectsProviderNativeAndNamedSourceTogether(t *testing.T) {
	t.Parallel()
	cfg := Config{
		ArtifactStores: map[string]ArtifactStoreSpec{
			"images": {Type: "local", Path: t.TempDir()},
		},
		Sources: map[string]SourceSpec{
			"base": {Store: "images", Path: "base.vhdx", Digest: "sha256:" + strings.Repeat("a", 64), Format: "vhdx", Provider: "hyperv"},
		},
		Pools: []PoolSpec{{Name: "windows", Type: "vm", Provider: "hyperv", Source: "base", Config: map[string]any{"template_vhd": `C:\\base.vhdx`}}},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "both provider-native source location") {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestConfigValidateRejectsLiteralS3Credentials(t *testing.T) {
	t.Parallel()
	cfg := Config{ArtifactStores: map[string]ArtifactStoreSpec{
		"remote": {Type: "s3", Bucket: "boxy", AccessKey: "literal", SecretKey: "env:BOXY_SECRET"},
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "access_key must be an env:NAME") {
		t.Fatalf("Validate error = %v", err)
	}
}
