package cli

import (
	"net/url"
	"sort"
	"strings"

	boxyconfig "github.com/Geogboe/boxy/internal/config"
	"github.com/Geogboe/boxy/internal/server"
	"github.com/Geogboe/boxy/pkg/resourcepack"
)

// catalogSnapshotFromConfig is the daemon boundary for the UI catalog. It
// copies only allowlisted, non-secret fields from configuration and receives
// already-resolved pools so the server never needs to inspect config maps or
// provider instances.
func catalogSnapshotFromConfig(cfg boxyconfig.Config, poolSpecs []boxyconfig.PoolSpec) server.CatalogSnapshot {
	snapshot := server.CatalogSnapshot{
		Templates: make([]server.CatalogTemplate, 0, len(cfg.Templates)),
		Packages:  make([]server.CatalogPackage, 0, len(cfg.Packages)),
		Sources:   make([]server.CatalogSourceEntry, 0, len(cfg.Sources)),
		Stores:    make([]server.CatalogStore, 0, len(cfg.ArtifactStores)),
		Pools:     make([]server.CatalogPool, 0, len(poolSpecs)),
	}
	for name := range cfg.Templates {
		resolved, err := cfg.ResolveTemplate(name)
		if err != nil {
			// Config validation runs before this boundary in runServe. Keep a
			// useful name if a direct unit test supplies an incomplete config.
			resolved.Name = name
		}
		snapshot.Templates = append(snapshot.Templates, server.CatalogTemplate{
			Name: resolved.Name, Extends: resolved.Extends, Type: resolved.Type,
			Provider: resolved.Provider, Agent: resolved.Agent, Source: resolved.Source,
			Packages: append([]string(nil), resolved.Packages...),
		})
	}
	for name, manifest := range cfg.Packages {
		if strings.TrimSpace(manifest.Name) == "" {
			manifest.Name = name
		}
		if compiled, err := resourcepack.Compile(manifest); err == nil {
			manifest = compiled
		}
		scopes := make([]string, 0, len(manifest.Scopes))
		for _, scope := range manifest.Scopes {
			scopes = append(scopes, string(scope))
		}
		events := make([]string, 0, len(manifest.Events))
		for _, event := range manifest.Events {
			events = append(events, string(event))
		}
		snapshot.Packages = append(snapshot.Packages, server.CatalogPackage{
			Name: manifest.Name, Version: manifest.Version, Method: string(manifest.Method),
			Scopes: scopes, Events: events,
		})
	}
	for name, source := range cfg.Sources {
		snapshot.Sources = append(snapshot.Sources, server.CatalogSourceEntry{
			Name: name, Store: source.Store, Path: source.Path, Digest: source.Digest,
			Format: source.Format, OS: source.OS, Provider: source.Provider,
		})
	}
	for name, store := range cfg.ArtifactStores {
		snapshot.Stores = append(snapshot.Stores, server.CatalogStore{
			Name: name, Type: store.Type, Endpoint: sanitizeCatalogEndpoint(store.Endpoint),
			Bucket: store.Bucket, Path: store.Path,
		})
	}
	for _, pool := range poolSpecs {
		snapshot.Pools = append(snapshot.Pools, server.CatalogPool{
			Name: pool.Name, Template: pool.Template, Type: pool.Type,
			Provider: pool.Provider, Agent: pool.Agent, Source: pool.Source,
			Packages: append([]string(nil), pool.Packages...),
		})
	}
	sort.Slice(snapshot.Templates, func(i, j int) bool { return snapshot.Templates[i].Name < snapshot.Templates[j].Name })
	sort.Slice(snapshot.Packages, func(i, j int) bool {
		if snapshot.Packages[i].Name == snapshot.Packages[j].Name {
			return snapshot.Packages[i].Version < snapshot.Packages[j].Version
		}
		return snapshot.Packages[i].Name < snapshot.Packages[j].Name
	})
	sort.Slice(snapshot.Sources, func(i, j int) bool { return snapshot.Sources[i].Name < snapshot.Sources[j].Name })
	sort.Slice(snapshot.Stores, func(i, j int) bool { return snapshot.Stores[i].Name < snapshot.Stores[j].Name })
	sort.Slice(snapshot.Pools, func(i, j int) bool { return snapshot.Pools[i].Name < snapshot.Pools[j].Name })
	return snapshot
}

const redactedCatalogEndpoint = "[redacted endpoint]"

// sanitizeCatalogEndpoint retains only the non-sensitive location portion of
// a URL before it crosses into the server's view model. A malformed or
// non-absolute endpoint is intentionally not guessed at: showing a generic
// marker is safer than accidentally rendering credentials in an opaque value.
func sanitizeCatalogEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return redactedCatalogEndpoint
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}
