package resourcespec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Spec is a provider-agnostic description of a resource's desired customization state.
//
// Base is immutable: it must match exactly for cache reuse or upgrades.
// Other fields are treated as sets/maps and normalized for stable comparison.
type Spec struct {
	Base BaseSpec `json:"base"`

	Packages      []string           `json:"packages,omitempty"`
	Groups        []string           `json:"groups,omitempty"`
	Users         []UserSpec         `json:"users,omitempty"`
	FirewallRules []FirewallRuleSpec `json:"firewall_rules,omitempty"`
	Services      []ServiceSpec      `json:"services,omitempty"`

	Labels map[string]string `json:"labels,omitempty"`
}

type BaseSpec struct {
	// Kind identifies the base artifact type (e.g. "docker.image", "hyperv.vhdx").
	Kind string `json:"kind"`
	// Ref is the provider-specific artifact identifier (image name, template path, etc).
	Ref string `json:"ref"`

	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
}

type UserSpec struct {
	Name   string   `json:"name"`
	Groups []string `json:"groups,omitempty"`
}

type FirewallRuleSpec struct {
	ID string `json:"id"`
}

type ServiceState string

const (
	ServiceEnabled  ServiceState = "enabled"
	ServiceDisabled ServiceState = "disabled"
)

type ServiceSpec struct {
	Name  string       `json:"name"`
	State ServiceState `json:"state"`
}

// Normalize returns a canonical representation of the spec suitable for stable
// comparison and hashing.
func Normalize(s Spec) Spec {
	s.Base.Kind = strings.TrimSpace(s.Base.Kind)
	s.Base.Ref = strings.TrimSpace(s.Base.Ref)
	s.Base.OS = strings.TrimSpace(s.Base.OS)
	s.Base.Arch = strings.TrimSpace(s.Base.Arch)

	s.Packages = normalizeStringSet(s.Packages)
	s.Groups = normalizeStringSet(s.Groups)
	s.FirewallRules = normalizeFirewallRules(s.FirewallRules)
	s.Services = normalizeServices(s.Services)
	s.Users = normalizeUsers(s.Users)

	s.Labels = normalizeStringMap(s.Labels)
	return s
}

// Digest returns a stable SHA-256 hash of the normalized spec.
func Digest(s Spec) (string, error) {
	c := canonicalize(Normalize(s))
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal canonical spec: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func Equal(a, b Spec) bool {
	return slices.EqualFunc(canonicalize(Normalize(a)), canonicalize(Normalize(b)), func(x, y canonicalSpec) bool {
		return x.equal(y)
	})
}

type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// canonicalSpec is a JSON-friendly canonical representation with maps converted
// into sorted slices for stable hashing.
type canonicalSpec struct {
	Base BaseSpec `json:"base"`

	Packages      []string           `json:"packages,omitempty"`
	Groups        []string           `json:"groups,omitempty"`
	Users         []UserSpec         `json:"users,omitempty"`
	FirewallRules []FirewallRuleSpec `json:"firewall_rules,omitempty"`
	Services      []ServiceSpec      `json:"services,omitempty"`

	Labels []KV `json:"labels,omitempty"`
}

func canonicalize(s Spec) []canonicalSpec {
	// This odd shape (slice of one) exists to allow slices.EqualFunc usage without
	// importing reflect. It also makes it harder to accidentally compare maps
	// non-deterministically.
	out := canonicalSpec{
		Base:          s.Base,
		Packages:      slices.Clone(s.Packages),
		Groups:        slices.Clone(s.Groups),
		Users:         cloneUsers(s.Users),
		FirewallRules: slices.Clone(s.FirewallRules),
		Services:      slices.Clone(s.Services),
		Labels:        mapToSortedKVs(s.Labels),
	}
	return []canonicalSpec{out}
}

func (c canonicalSpec) equal(o canonicalSpec) bool {
	if c.Base != o.Base {
		return false
	}
	if !slices.Equal(c.Packages, o.Packages) {
		return false
	}
	if !slices.Equal(c.Groups, o.Groups) {
		return false
	}
	if !usersEqual(c.Users, o.Users) {
		return false
	}
	if !slices.Equal(c.FirewallRules, o.FirewallRules) {
		return false
	}
	if !slices.Equal(c.Services, o.Services) {
		return false
	}
	return slices.Equal(c.Labels, o.Labels)
}

func normalizeStringSet(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mapToSortedKVs(m map[string]string) []KV {
	if len(m) == 0 {
		return nil
	}
	kvs := make([]KV, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, KV{Key: k, Value: v})
	}
	slices.SortFunc(kvs, func(a, b KV) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		if a.Value < b.Value {
			return -1
		}
		if a.Value > b.Value {
			return 1
		}
		return 0
	})
	return kvs
}

func normalizeFirewallRules(in []FirewallRuleSpec) []FirewallRuleSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]FirewallRuleSpec, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, FirewallRuleSpec{ID: id})
	}
	slices.SortFunc(out, func(a, b FirewallRuleSpec) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeServices(in []ServiceSpec) []ServiceSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]ServiceSpec, 0, len(in))
	seen := make(map[string]ServiceSpec, len(in))
	for _, s := range in {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		state := s.State
		if state == "" {
			state = ServiceEnabled
		}
		seen[name] = ServiceSpec{Name: name, State: state}
	}
	for _, v := range seen {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b ServiceSpec) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		if a.State < b.State {
			return -1
		}
		if a.State > b.State {
			return 1
		}
		return 0
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeUsers(in []UserSpec) []UserSpec {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]UserSpec, len(in))
	for _, u := range in {
		name := strings.TrimSpace(u.Name)
		if name == "" {
			continue
		}
		seen[name] = UserSpec{
			Name:   name,
			Groups: normalizeStringSet(u.Groups),
		}
	}
	out := make([]UserSpec, 0, len(seen))
	for _, u := range seen {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b UserSpec) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		// Groups already normalized; compare by joined string for determinism.
		aj := strings.Join(a.Groups, "\x00")
		bj := strings.Join(b.Groups, "\x00")
		if aj < bj {
			return -1
		}
		if aj > bj {
			return 1
		}
		return 0
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneUsers(in []UserSpec) []UserSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]UserSpec, 0, len(in))
	for _, u := range in {
		out = append(out, UserSpec{Name: u.Name, Groups: slices.Clone(u.Groups)})
	}
	return out
}

func usersEqual(a, b []UserSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
		if !slices.Equal(a[i].Groups, b[i].Groups) {
			return false
		}
	}
	return true
}
