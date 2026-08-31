package server

import (
	"context"
	"sort"
	"strings"

	"github.com/Geogboe/boxy/pkg/artifact"
)

// CatalogSource is the narrow, read-only seam between daemon configuration
// and the dashboard catalog. Implementations should return a startup snapshot
// rather than exposing mutable configuration or provider objects.
type CatalogSource interface {
	LoadCatalog(ctx context.Context) (CatalogSnapshot, error)
}

// CatalogSnapshot contains only fields that are safe and useful to render in
// the operator catalog. Sensitive configuration (credentials, secret
// references, arbitrary config maps, and source metadata) deliberately has no
// field in this view model.
type CatalogSnapshot struct {
	Templates []CatalogTemplate
	Packages  []CatalogPackage
	Sources   []CatalogSourceEntry
	Stores    []CatalogStore
	Pools     []CatalogPool
}

type CatalogTemplate struct {
	Name              string
	Extends           string
	Type              string
	Provider          string
	Agent             string
	Source            string
	Packages          []string
	MissingReferences []string
}

type CatalogPackage struct {
	Name              string
	Version           string
	Method            string
	Scopes            []string
	Events            []string
	MissingReferences []string
}

type CatalogSourceEntry struct {
	Name              string
	Store             string
	Path              string
	Digest            string
	Format            string
	OS                string
	Provider          string
	MissingReferences []string
}

type CatalogStore struct {
	Name     string
	Type     string
	Endpoint string
	Bucket   string
	Path     string
}

type CatalogPool struct {
	Name              string
	Template          string
	Type              string
	Provider          string
	Agent             string
	Source            string
	Packages          []string
	MissingReferences []string
}

type staticCatalogSource struct {
	snapshot CatalogSnapshot
}

// NewStaticCatalogSource wraps an immutable copy of snapshot for daemon
// wiring and tests. Every load returns another copy so neither the server nor
// a caller can mutate the stored startup snapshot through shared slices.
func NewStaticCatalogSource(snapshot CatalogSnapshot) CatalogSource {
	return staticCatalogSource{snapshot: normalizeCatalog(snapshot)}
}

func (s staticCatalogSource) LoadCatalog(_ context.Context) (CatalogSnapshot, error) {
	return cloneCatalog(s.snapshot), nil
}

type catalogPageData struct {
	LoadError string
	Empty     bool
	Templates []CatalogTemplate
	Packages  []CatalogPackage
	Sources   []CatalogSourceEntry
	Stores    []CatalogStore
	Pools     []CatalogPool
}

func catalogPage(snapshot CatalogSnapshot) catalogPageData {
	snapshot = normalizeCatalog(snapshot)
	return catalogPageData{
		Empty:     len(snapshot.Templates) == 0 && len(snapshot.Packages) == 0 && len(snapshot.Sources) == 0 && len(snapshot.Stores) == 0 && len(snapshot.Pools) == 0,
		Templates: snapshot.Templates,
		Packages:  snapshot.Packages,
		Sources:   snapshot.Sources,
		Stores:    snapshot.Stores,
		Pools:     snapshot.Pools,
	}
}

func normalizeCatalog(snapshot CatalogSnapshot) CatalogSnapshot {
	snapshot = cloneCatalog(snapshot)
	sort.SliceStable(snapshot.Templates, func(i, j int) bool { return snapshot.Templates[i].Name < snapshot.Templates[j].Name })
	sort.SliceStable(snapshot.Packages, func(i, j int) bool {
		left, right := snapshot.Packages[i], snapshot.Packages[j]
		if left.Name == right.Name {
			return left.Version < right.Version
		}
		return left.Name < right.Name
	})
	sort.SliceStable(snapshot.Sources, func(i, j int) bool { return snapshot.Sources[i].Name < snapshot.Sources[j].Name })
	sort.SliceStable(snapshot.Stores, func(i, j int) bool { return snapshot.Stores[i].Name < snapshot.Stores[j].Name })
	sort.SliceStable(snapshot.Pools, func(i, j int) bool { return snapshot.Pools[i].Name < snapshot.Pools[j].Name })

	templates := make(map[string]struct{}, len(snapshot.Templates))
	packages := make(map[string]struct{}, len(snapshot.Packages))
	sources := make(map[string]struct{}, len(snapshot.Sources))
	stores := make(map[string]struct{}, len(snapshot.Stores))
	for _, entry := range snapshot.Templates {
		templates[entry.Name] = struct{}{}
	}
	for _, entry := range snapshot.Packages {
		packages[entry.Name+"@"+entry.Version] = struct{}{}
	}
	for _, entry := range snapshot.Sources {
		sources[entry.Name] = struct{}{}
	}
	for _, entry := range snapshot.Stores {
		stores[entry.Name] = struct{}{}
	}

	for i := range snapshot.Templates {
		entry := &snapshot.Templates[i]
		entry.MissingReferences = nil
		addMissing(&entry.MissingReferences, entry.Extends, templates)
		addMissing(&entry.MissingReferences, entry.Source, sources)
		addMissingPackages(&entry.MissingReferences, entry.Packages, packages)
	}
	for i := range snapshot.Sources {
		entry := &snapshot.Sources[i]
		entry.MissingReferences = nil
		addMissing(&entry.MissingReferences, entry.Store, stores)
	}
	for i := range snapshot.Pools {
		entry := &snapshot.Pools[i]
		entry.MissingReferences = nil
		addMissing(&entry.MissingReferences, entry.Template, templates)
		addMissing(&entry.MissingReferences, entry.Source, sources)
		addMissingPackages(&entry.MissingReferences, entry.Packages, packages)
	}
	return snapshot
}

func addMissing(missing *[]string, value string, known map[string]struct{}) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := known[value]; !ok {
		*missing = append(*missing, value)
	}
}

func addMissingPackages(missing *[]string, refs []string, known map[string]struct{}) {
	for _, raw := range refs {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		canonical := ref
		if parsed, err := artifact.ParseRef(ref); err == nil {
			canonical = parsed.String()
		}
		if _, ok := known[canonical]; !ok {
			*missing = append(*missing, ref)
		}
	}
	sort.Strings(*missing)
}

func cloneCatalog(snapshot CatalogSnapshot) CatalogSnapshot {
	clone := snapshot
	clone.Templates = make([]CatalogTemplate, len(snapshot.Templates))
	for i, entry := range snapshot.Templates {
		clone.Templates[i] = entry
		clone.Templates[i].Packages = append([]string(nil), entry.Packages...)
		clone.Templates[i].MissingReferences = append([]string(nil), entry.MissingReferences...)
	}
	clone.Packages = make([]CatalogPackage, len(snapshot.Packages))
	for i, entry := range snapshot.Packages {
		clone.Packages[i] = entry
		clone.Packages[i].Scopes = append([]string(nil), entry.Scopes...)
		clone.Packages[i].Events = append([]string(nil), entry.Events...)
		clone.Packages[i].MissingReferences = append([]string(nil), entry.MissingReferences...)
	}
	clone.Sources = make([]CatalogSourceEntry, len(snapshot.Sources))
	for i, entry := range snapshot.Sources {
		clone.Sources[i] = entry
		clone.Sources[i].MissingReferences = append([]string(nil), entry.MissingReferences...)
	}
	clone.Stores = append([]CatalogStore(nil), snapshot.Stores...)
	clone.Pools = make([]CatalogPool, len(snapshot.Pools))
	for i, entry := range snapshot.Pools {
		clone.Pools[i] = entry
		clone.Pools[i].Packages = append([]string(nil), entry.Packages...)
		clone.Pools[i].MissingReferences = append([]string(nil), entry.MissingReferences...)
	}
	return clone
}
