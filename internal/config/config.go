package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Geogboe/boxy/pkg/artifact"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"gopkg.in/yaml.v3"
)

// Config is the top-level Boxy configuration file structure.
//
// Keep this intentionally small while the CLI wiring lands. Expand as core
// managers gain real behavior.
type Config struct {
	Providers      []providersdk.Instance           `json:"providers" yaml:"providers"`
	Pools          []PoolSpec                       `json:"pools,omitempty" yaml:"pools,omitempty"`
	Templates      map[string]TemplateSpec          `json:"templates,omitempty" yaml:"templates,omitempty"`
	Sources        map[string]SourceSpec            `json:"sources,omitempty" yaml:"sources,omitempty"`
	ArtifactStores map[string]ArtifactStoreSpec     `json:"artifact_stores,omitempty" yaml:"artifact_stores,omitempty"`
	Packages       map[string]resourcepack.Manifest `json:"packages,omitempty" yaml:"packages,omitempty"`

	Server ServerSpec `json:"server,omitzero" yaml:"server,omitempty"`
}

type ServerSpec struct {
	Listen    string   `json:"listen,omitempty" yaml:"listen,omitempty"`
	Providers []string `json:"providers,omitempty" yaml:"providers,omitempty"`

	// UI controls whether the web dashboard is served alongside the API.
	// Pointer so nil = default (enabled). Set to false to disable.
	UI *bool `json:"ui,omitempty" yaml:"ui,omitempty"`

	// GRPCListen is the address the agent-transport gRPC server listens
	// on (see docs/adr/0005-remote-agent-transport-and-registration.md).
	// Empty means the default (":9091").
	GRPCListen string `json:"grpc_listen,omitempty" yaml:"grpc_listen,omitempty"`

	// AgentHeartbeatInterval is how often connected remote agents send
	// heartbeats, as a Go duration string (e.g. "15s"). Empty means the
	// default (15s). Note: --insecure/--dev is deliberately a CLI flag
	// only, never a config field, so a stale or copy-pasted config file
	// can't silently disable mTLS in a real deployment.
	AgentHeartbeatInterval string `json:"agent_heartbeat_interval,omitempty" yaml:"agent_heartbeat_interval,omitempty"`

	// GRPCCertSANs are extra DNS names/IPs to include in the agent gRPC
	// server certificate's Subject Alternative Names, on top of the
	// always-included localhost/127.0.0.1/listen-host entries. Needed when
	// remote agents connect through a passthrough route or load balancer
	// using an external DNS name that doesn't match the literal
	// --grpc-listen host. Unlike --insecure above, this is safe to expose
	// as a config field: adding a SAN never weakens TLS/mTLS verification,
	// it only widens which hostname a client may present. Equivalent
	// repeatable CLI flag: --grpc-cert-san (fully overrides this value
	// when passed, does not merge with it).
	GRPCCertSANs []string `json:"grpc_cert_sans,omitempty" yaml:"grpc_cert_sans,omitempty"`

	// Secrets selects the server-owned credential backend. It is required when
	// a provider admission policy needs guest credentials; it is intentionally
	// not defaulted so deployment posture is explicit.
	Secrets SecretSpec `json:"secrets,omitzero" yaml:"secrets,omitempty"`

	// OIDC configures browser login against an external OpenID Connect
	// provider. Not configured by default: the web UI's bootstrapped local
	// admin account (see internal/cli/serve.go's bootstrapLocalAdmin) is
	// always available as a fallback/break-glass login regardless of this
	// setting. See docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md.
	OIDC OIDCSpec `json:"oidc,omitzero" yaml:"oidc,omitempty"`
}

// OIDCSpec configures browser and CLI login against an external OpenID
// Connect provider.
type OIDCSpec struct {
	// Issuer is the provider's issuer URL (e.g.
	// "https://keycloak.example.invalid/realms/boxy"), used both as the
	// discovery-document base and the expected "iss" claim value.
	Issuer string `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	// ClientID is the OAuth2 client ID registered with the provider for
	// this boxy daemon.
	ClientID string `json:"client_id,omitempty" yaml:"client_id,omitempty"`
	// ClientSecret must be an "env:NAME" reference (see
	// pkg/providersdk.ResolveSecretRef), never a literal value -- the raw
	// secret must not live in a config file that might be committed or
	// copied around. Resolved once at daemon startup.
	ClientSecret string `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
	// RedirectURL is this daemon's own callback URL (e.g.
	// "https://boxy.example.invalid/auth/callback"), registered with the
	// provider as an allowed redirect target.
	RedirectURL string `json:"redirect_url,omitempty" yaml:"redirect_url,omitempty"`
	// RoleClaim names the ID token claim (e.g. "groups", "roles") whose
	// value(s) are looked up in RoleMapping to resolve a Boxy role. The
	// claim may be a single string or an array of strings (e.g. a
	// "groups" claim); every matching value's mapped role is considered
	// and the most-privileged one wins (admin > auditor > user) so a
	// principal in multiple groups isn't order-dependent on the
	// provider's own claim ordering.
	RoleClaim string `json:"role_claim,omitempty" yaml:"role_claim,omitempty"`
	// RoleMapping maps a RoleClaim value to a Boxy role
	// (user/auditor/admin).
	RoleMapping map[string]string `json:"role_mapping,omitempty" yaml:"role_mapping,omitempty"`
	// DefaultRole is used when no RoleClaim value matches RoleMapping.
	// Empty (the default) fails closed: a login with no mapped role is
	// rejected rather than silently granted the lowest role, since "IdP
	// claims drifted" and "this person genuinely has no boxy role" must
	// not look the same as a working login.
	DefaultRole string `json:"default_role,omitempty" yaml:"default_role,omitempty"`

	// CLIClientID, if set, enables `boxy login --oidc`: a public
	// (no-secret) OAuth2 client registered with the provider for the
	// RFC 8628 device-authorization grant, distinct from ClientID (the
	// confidential web client) since a CLI binary cannot safely hold a
	// client secret. Empty means CLI OIDC login is unavailable; the web
	// UI login above is unaffected either way.
	CLIClientID string `json:"cli_client_id,omitempty" yaml:"cli_client_id,omitempty"`
	// PersonalKeyMaxTTL bounds how long a self-service personal API key
	// (minted via CLI device-code login) may live, as a Go duration
	// string (e.g. "12h"). Empty defaults to 12h.
	PersonalKeyMaxTTL string `json:"personal_key_max_ttl,omitempty" yaml:"personal_key_max_ttl,omitempty"`
	// SessionTTL bounds how long a web-UI login session lasts before its
	// cookie is rejected, as a Go duration string (e.g. "12h"). Empty
	// defaults to 12h. Applies to every session regardless of how it was
	// established (OIDC or the bootstrapped local-admin account) -- there
	// is only one session mechanism (see ADR-0016), even though this knob
	// lives under server.oidc for parity with PersonalKeyMaxTTL.
	SessionTTL string `json:"session_ttl,omitempty" yaml:"session_ttl,omitempty"`
}

// Configured reports whether OIDC login is enabled.
func (o OIDCSpec) Configured() bool {
	return strings.TrimSpace(o.Issuer) != ""
}

// EffectivePersonalKeyMaxTTL parses PersonalKeyMaxTTL, defaulting to 12h.
func (o OIDCSpec) EffectivePersonalKeyMaxTTL() (time.Duration, error) {
	if strings.TrimSpace(o.PersonalKeyMaxTTL) == "" {
		return 12 * time.Hour, nil
	}
	d, err := time.ParseDuration(o.PersonalKeyMaxTTL)
	if err != nil {
		return 0, fmt.Errorf("server.oidc.personal_key_max_ttl: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("server.oidc.personal_key_max_ttl must be a positive duration")
	}
	return d, nil
}

// EffectiveSessionTTL parses SessionTTL, defaulting to 12h.
func (o OIDCSpec) EffectiveSessionTTL() (time.Duration, error) {
	if strings.TrimSpace(o.SessionTTL) == "" {
		return 12 * time.Hour, nil
	}
	d, err := time.ParseDuration(o.SessionTTL)
	if err != nil {
		return 0, fmt.Errorf("server.oidc.session_ttl: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("server.oidc.session_ttl must be a positive duration")
	}
	return d, nil
}

// Validate checks field presence/shape. It does not resolve ClientSecret or
// contact the issuer -- see internal/cli/serve.go's OIDC wiring for that.
// SessionTTL is validated unconditionally, below, since it applies even
// when OIDC itself is not configured.
func (o OIDCSpec) Validate() error {
	if _, err := o.EffectiveSessionTTL(); err != nil {
		return err
	}
	if !o.Configured() {
		return nil
	}
	if strings.TrimSpace(o.ClientID) == "" {
		return fmt.Errorf("server.oidc.client_id is required when server.oidc.issuer is set")
	}
	if !strings.HasPrefix(strings.TrimSpace(o.ClientSecret), "env:") {
		return fmt.Errorf(`server.oidc.client_secret must be an "env:NAME" reference, not a literal value`)
	}
	if strings.TrimSpace(o.RedirectURL) == "" {
		return fmt.Errorf("server.oidc.redirect_url is required when server.oidc.issuer is set")
	}
	if strings.TrimSpace(o.RoleClaim) == "" {
		return fmt.Errorf("server.oidc.role_claim is required when server.oidc.issuer is set")
	}
	if len(o.RoleMapping) == 0 {
		return fmt.Errorf("server.oidc.role_mapping must have at least one entry when server.oidc.issuer is set")
	}
	for claimValue, role := range o.RoleMapping {
		if !model.APIKeyRole(role).Valid() {
			return fmt.Errorf("server.oidc.role_mapping[%q] = %q is not a valid role", claimValue, role)
		}
	}
	if o.DefaultRole != "" && !model.APIKeyRole(o.DefaultRole).Valid() {
		return fmt.Errorf("server.oidc.default_role %q is not a valid role", o.DefaultRole)
	}
	if _, err := o.EffectivePersonalKeyMaxTTL(); err != nil {
		return err
	}
	return nil
}

// SecretSpec configures the server-owned secret backend.
type SecretSpec struct {
	Backend string `json:"backend,omitempty" yaml:"backend,omitempty"`
	Path    string `json:"path,omitempty" yaml:"path,omitempty"`
	Service string `json:"service,omitempty" yaml:"service,omitempty"`
}

func (s SecretSpec) Config() boxysecrets.Config {
	return boxysecrets.Config{
		Backend: boxysecrets.Backend(strings.ToLower(strings.TrimSpace(s.Backend))),
		Path:    strings.TrimSpace(s.Path),
		Service: strings.TrimSpace(s.Service),
	}
}

func (s SecretSpec) Configured() bool {
	return strings.TrimSpace(s.Backend) != ""
}

func (s SecretSpec) Validate() error {
	if !s.Configured() {
		if strings.TrimSpace(s.Path) != "" || strings.TrimSpace(s.Service) != "" {
			return fmt.Errorf("server.secrets.backend is required when secret settings are present")
		}
		return nil
	}
	cfg := s.Config()
	switch cfg.Backend {
	case boxysecrets.BackendFile, boxysecrets.BackendDPAPI:
		if cfg.Path == "" {
			return fmt.Errorf("server.secrets.path is required for backend %q", cfg.Backend)
		}
	case boxysecrets.BackendKeyring:
		if cfg.Path != "" {
			return fmt.Errorf("server.secrets.path is not valid for keyring backend")
		}
	case boxysecrets.Backend(""):
		return fmt.Errorf("server.secrets.backend is required")
	default:
		return fmt.Errorf("unsupported server.secrets.backend %q", cfg.Backend)
	}
	return nil
}

// UIEnabled reports whether the web UI should be served.
// Returns true when UI is nil (unset) or explicitly true.
func (s ServerSpec) UIEnabled() bool {
	return s.UI == nil || *s.UI
}

// DefaultAgentHeartbeatInterval is used when agent_heartbeat_interval is
// unset — close to the daemon's existing 10s reconcile tick.
const DefaultAgentHeartbeatInterval = 15 * time.Second

// EffectiveAgentHeartbeatInterval parses AgentHeartbeatInterval, applying
// the default when unset. Invalid values error (Validate also rejects them
// at load time, so a running daemon should never hit that path).
func (s ServerSpec) EffectiveAgentHeartbeatInterval() (time.Duration, error) {
	if strings.TrimSpace(s.AgentHeartbeatInterval) == "" {
		return DefaultAgentHeartbeatInterval, nil
	}
	d, err := time.ParseDuration(s.AgentHeartbeatInterval)
	if err != nil {
		return 0, fmt.Errorf("agent_heartbeat_interval %q: %w", s.AgentHeartbeatInterval, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("agent_heartbeat_interval %q must be positive", s.AgentHeartbeatInterval)
	}
	return d, nil
}

func LoadFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	switch ext := filepath.Ext(path); ext {
	case ".yaml", ".yml":
		return decodeYAML(b)
	case ".json":
		return decodeJSON(b)
	default:
		return Config{}, fmt.Errorf("unsupported config extension %q (supported: .yaml, .yml, .json)", ext)
	}
}

func decodeYAML(b []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if err == io.EOF {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("decode yaml: %w", err)
	}
	return cfg, nil
}

func decodeJSON(b []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		if err == io.EOF {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("decode json: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("decode json: unexpected extra content after document")
	} else if err != io.EOF {
		return fmt.Errorf("decode json: trailing content: %w", err)
	}
	return nil
}

// Validate checks semantic config constraints that decoding alone does not enforce.
func (c Config) Validate() error {
	if _, err := c.Server.EffectiveAgentHeartbeatInterval(); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := c.Server.Secrets.Validate(); err != nil {
		return err
	}
	if err := c.Server.OIDC.Validate(); err != nil {
		return err
	}
	for i, san := range c.Server.GRPCCertSANs {
		if strings.TrimSpace(san) == "" {
			return fmt.Errorf("server: grpc_cert_sans[%d] must not be empty", i)
		}
	}
	for name, store := range c.ArtifactStores {
		switch strings.ToLower(strings.TrimSpace(store.Type)) {
		case "local", "filesystem", "s3":
		default:
			return fmt.Errorf("artifact store %q has unsupported type %q", name, store.Type)
		}
	}
	for name, source := range c.Sources {
		if _, ok := c.ArtifactStores[source.Store]; !ok {
			return fmt.Errorf("source %q references unknown artifact store %q", name, source.Store)
		}
	}
	for name, manifest := range c.Packages {
		if manifest.Name != "" && strings.TrimSpace(manifest.Name) != strings.TrimSpace(name) {
			return fmt.Errorf("package %q manifest name is %q", name, manifest.Name)
		}
		if manifest.Name == "" {
			manifest.Name = name
		}
		if err := manifest.Validate(); err != nil {
			return fmt.Errorf("package %q: %w", name, err)
		}
	}
	for name := range c.Templates {
		resolved, err := c.ResolveTemplate(name)
		if err != nil {
			return fmt.Errorf("template %q: %w", name, err)
		}
		if resolved.Source != "" {
			if _, ok := c.Sources[resolved.Source]; !ok {
				return fmt.Errorf("template %q references unknown source %q", name, resolved.Source)
			}
		}
	}
	for _, pool := range c.Pools {
		if strings.TrimSpace(pool.Template) != "" {
			resolved, err := c.ResolveTemplate(pool.Template)
			if err != nil {
				return fmt.Errorf("pool %q template: %w", pool.Name, err)
			}
			if pool.Type != "" {
				templateType, err := ResolvePoolExpectedType(resolved.Type)
				if err != nil {
					return fmt.Errorf("pool %q template type: %w", pool.Name, err)
				}
				poolType, err := ResolvePoolExpectedType(pool.Type)
				if err != nil {
					return fmt.Errorf("pool %q type invalid: %w", pool.Name, err)
				}
				if poolType != templateType {
					return fmt.Errorf("pool %q type %q conflicts with template %q type %q", pool.Name, pool.Type, pool.Template, resolved.Type)
				}
			}
		}
		if _, err := ResolvePoolExpectedType(pool.Type); err != nil {
			return fmt.Errorf("pool %q type invalid: %w", pool.Name, err)
		}
		resolvedPool, err := c.ResolvePoolSpec(pool)
		if err != nil {
			return fmt.Errorf("pool %q: %w", pool.Name, err)
		}
		if resolvedPool.Source != "" {
			if _, ok := c.Sources[resolvedPool.Source]; !ok {
				return fmt.Errorf("pool %q references unknown source %q", pool.Name, resolvedPool.Source)
			}
		}
		if err := c.validatePackageRefs(resolvedPool.Packages, resourcepack.ScopeResource, resourcepack.EventProvision); err != nil {
			return fmt.Errorf("pool %q packages: %w", pool.Name, err)
		}
		if pool.PolicySet() && pool.PoliciesSet() {
			return fmt.Errorf("pool %q sets both policy and policies; use only one", pool.Name)
		}
		policy := pool.EffectivePolicy()
		if policy.Preheat.ConfiguresDrain() && policy.Preheat.MinReady > 0 {
			return fmt.Errorf("pool %q preheat max_total: 0 conflicts with min_ready: %d", pool.Name, policy.Preheat.MinReady)
		}
	}
	return nil
}

func (c Config) validatePackageRefs(refs []string, scope resourcepack.Scope, event resourcepack.Event) error {
	for _, rawRef := range refs {
		ref, err := artifact.ParseRef(rawRef)
		if err != nil {
			return err
		}
		manifest, ok := c.Packages[ref.Name]
		if !ok {
			return fmt.Errorf("package %q is not configured", rawRef)
		}
		if manifest.Name == "" {
			manifest.Name = ref.Name
		}
		if manifest.Version != ref.Version {
			return fmt.Errorf("package %q version does not match configured version %q", rawRef, manifest.Version)
		}
		if err := manifest.Validate(); err != nil {
			return err
		}
		if !containsPackageScope(manifest.Scopes, scope) {
			return fmt.Errorf("package %q does not declare scope %q", rawRef, scope)
		}
		if !containsPackageEvent(manifest.Events, event) {
			return fmt.Errorf("package %q does not declare event %q", rawRef, event)
		}
	}
	return nil
}

func containsPackageScope(scopes []resourcepack.Scope, want resourcepack.Scope) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

func containsPackageEvent(events []resourcepack.Event, want resourcepack.Event) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

// ResolvePoolExpectedType maps a config pool type to the runtime resource type.
func ResolvePoolExpectedType(t string) (model.ResourceType, error) {
	switch strings.TrimSpace(t) {
	case "", "container", "docker":
		return model.ResourceTypeContainer, nil
	case "vm":
		return model.ResourceTypeVM, nil
	case "share":
		return model.ResourceTypeShare, nil
	default:
		return model.ResourceTypeUnknown, fmt.Errorf("unsupported pool type %q", t)
	}
}
