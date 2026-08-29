package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/providersdk/providers/hyperv"
	boxysecrets "github.com/Geogboe/boxy/pkg/secrets"
	"github.com/Geogboe/boxy/pkg/store"
)

func openConfiguredSecretStore(spec boxyconfig.SecretSpec, statePath string, required bool) (boxysecrets.Store, error) {
	if !spec.Configured() {
		if required {
			return nil, fmt.Errorf("server.secrets.backend is required for guest-personalizable pools")
		}
		return nil, nil
	}
	cfg := spec.Config()
	if cfg.Path != "" && !filepath.IsAbs(cfg.Path) {
		cfg.Path = filepath.Join(filepath.Dir(statePath), cfg.Path)
	}
	secretsStore, err := boxysecrets.Open(cfg)
	if err != nil {
		return nil, err
	}
	if err := secretsStore.Check(); err != nil {
		return nil, fmt.Errorf("check secret backend: %w", err)
	}
	return secretsStore, nil
}

func requiresGuestSecretBackend(cfg boxyconfig.Config) bool {
	poolSpecs, err := cfg.ResolvePoolSpecs()
	if err != nil {
		return false
	}
	providerTypes := make(map[string]string, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		providerTypes[provider.Name] = strings.ToLower(strings.TrimSpace(string(provider.Type)))
	}
	for _, pool := range poolSpecs {
		poolType := strings.ToLower(strings.TrimSpace(pool.Type))
		providerType := providerTypes[pool.Provider]
		if poolType == "hyperv" || providerType == "hyperv" || (poolType == "vm" && providerType == "") {
			return true
		}
	}
	return false
}

func resolveGuestBootstrap(
	ctx context.Context,
	st store.Store,
	guestSecrets boxysecrets.Store,
	specs map[model.PoolName]boxyconfig.PoolSpec,
	resource model.Resource,
) (providersdk.GuestBootstrapCredential, error) {
	username := guestUsername(resource)
	if guestSecrets != nil {
		resourceValue, err := guestSecrets.Get(ctx, boxysecrets.ResourceCredentialKey(string(resource.ID)))
		if err == nil {
			return decodeStoredGuestCredential(resourceValue, username)
		}
		if !errors.Is(err, boxysecrets.ErrNotFound) {
			return providersdk.GuestBootstrapCredential{}, fmt.Errorf("get resource guest credential: %w", err)
		}
		poolValue, err := guestSecrets.Get(ctx, boxysecrets.PoolBootstrapKey(string(resource.OriginPool)))
		if err != nil {
			if errors.Is(err, boxysecrets.ErrNotFound) {
				return providersdk.GuestBootstrapCredential{}, fmt.Errorf("%w: pool guest bootstrap credential is not configured", store.ErrNotFound)
			}
			return providersdk.GuestBootstrapCredential{}, fmt.Errorf("get pool guest bootstrap credential: %w", err)
		}
		if strings.TrimSpace(string(poolValue)) == "" {
			return providersdk.GuestBootstrapCredential{}, fmt.Errorf("pool guest bootstrap credential is empty")
		}
		return providersdk.GuestBootstrapCredential{Username: username, Password: string(poolValue)}, nil
	}

	password, err := st.GetPoolGuestCredential(ctx, resource.OriginPool)
	if err == nil {
		return providersdk.GuestBootstrapCredential{Username: username, Password: password}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("get pool guest credential: %w", err)
	}

	spec, ok := specs[resource.OriginPool]
	if !ok {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("pool %q is not configured", resource.OriginPool)
	}
	if !strings.EqualFold(strings.TrimSpace(spec.Type), "hyperv") && !strings.EqualFold(strings.TrimSpace(spec.Type), "vm") {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("pool %q is not a Hyper-V pool", resource.OriginPool)
	}
	config := &hyperv.CreateConfig{}
	if len(spec.Config) != 0 {
		raw, err := json.Marshal(spec.Config)
		if err != nil {
			return providersdk.GuestBootstrapCredential{}, fmt.Errorf("marshal pool %q config: %w", resource.OriginPool, err)
		}
		if err := json.Unmarshal(raw, config); err != nil {
			return providersdk.GuestBootstrapCredential{}, fmt.Errorf("decode pool %q config: %w", resource.OriginPool, err)
		}
	}
	password, err = providersdk.ResolveSecretRef(ctx, providersdk.SecretRef(config.GuestPasswordRef))
	if err != nil {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("resolve legacy guest_password_ref: %w", err)
	}
	return providersdk.GuestBootstrapCredential{Username: username, Password: password}, nil
}

func decodeStoredGuestCredential(raw []byte, defaultUsername string) (providersdk.GuestBootstrapCredential, error) {
	var credential providersdk.GuestCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("decode resource guest credential: %w", err)
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(credential.Data, &payload); err != nil {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("decode resource password: %w", err)
	}
	if strings.TrimSpace(payload.Password) == "" {
		return providersdk.GuestBootstrapCredential{}, fmt.Errorf("resource guest credential has an empty password")
	}
	if strings.TrimSpace(payload.Username) == "" {
		payload.Username = defaultUsername
	}
	return providersdk.GuestBootstrapCredential{Username: payload.Username, Password: payload.Password}, nil
}

func guestUsername(resource model.Resource) string {
	if username, ok := resource.Properties["guest_user"].(string); ok && strings.TrimSpace(username) != "" {
		return username
	}
	if guestOS, ok := resource.Properties["guest_os"].(string); ok && strings.EqualFold(guestOS, "linux") {
		return "admin"
	}
	return "Administrator"
}
