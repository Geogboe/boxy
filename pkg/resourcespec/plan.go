package resourcespec

import (
	"fmt"
	"slices"
)

// Op is a pure, declarative customization operation.
//
// Execution is intentionally out of scope for this package.
type Op interface {
	OpName() string
}

type InstallPackage struct{ Name string }

func (InstallPackage) OpName() string { return "install_package" }

type EnsureGroup struct{ Name string }

func (EnsureGroup) OpName() string { return "ensure_group" }

type EnsureUser struct {
	Name   string
	Groups []string
}

func (EnsureUser) OpName() string { return "ensure_user" }

type AddFirewallRule struct{ ID string }

func (AddFirewallRule) OpName() string { return "add_firewall_rule" }

type EnableService struct{ Name string }

func (EnableService) OpName() string { return "enable_service" }

// PlanUpgrade returns a list of monotonic operations that would reconcile "from"
// to exactly match "to". It rejects non-monotonic transitions (removals,
// disabling services, changing the immutable base, etc).
//
// Note: This plans transitions between declared specs, not actual system state.
func PlanUpgrade(from, to Spec) ([]Op, error) {
	from = Normalize(from)
	to = Normalize(to)

	if from.Base != to.Base {
		return nil, fmt.Errorf("immutable base mismatch")
	}

	var ops []Op

	// Packages: add-only.
	if !isSubset(from.Packages, to.Packages) {
		return nil, fmt.Errorf("packages removal is not allowed")
	}
	for _, p := range missing(from.Packages, to.Packages) {
		ops = append(ops, InstallPackage{Name: p})
	}

	// Firewall rules: add-only.
	fromFR := firewallRuleIDs(from.FirewallRules)
	toFR := firewallRuleIDs(to.FirewallRules)
	if !isSubset(fromFR, toFR) {
		return nil, fmt.Errorf("firewall rule removal is not allowed")
	}
	for _, id := range missing(fromFR, toFR) {
		ops = append(ops, AddFirewallRule{ID: id})
	}

	// Services: enabling is allowed; disabling is rejected.
	fromSvc := servicesToMap(from.Services)
	toSvc := servicesToMap(to.Services)
	for name, desired := range toSvc {
		current, ok := fromSvc[name]
		if !ok {
			// Treat missing as "disabled" in declared-spec transitions.
			current = ServiceDisabled
		}
		switch desired {
		case ServiceEnabled:
			if current != ServiceEnabled {
				ops = append(ops, EnableService{Name: name})
			}
		case ServiceDisabled:
			if current == ServiceEnabled {
				return nil, fmt.Errorf("service %q disable is not allowed", name)
			}
		default:
			return nil, fmt.Errorf("service %q has invalid desired state %q", name, desired)
		}
	}
	// If from has a service enabled that to wants disabled, the loop above catches it.
	// If from specifies extra services, that's fine as long as it doesn't violate
	// to's desired disabled state.

	// Groups: add-only (but if from has extras not in to, cannot reach exact spec).
	if !isSubset(from.Groups, to.Groups) {
		return nil, fmt.Errorf("group removal is not allowed")
	}
	for _, g := range missing(from.Groups, to.Groups) {
		ops = append(ops, EnsureGroup{Name: g})
	}

	// Users: add-only; group membership add-only.
	fromUsers := usersToMap(from.Users)
	toUsers := usersToMap(to.Users)
	for name, desired := range toUsers {
		cur, ok := fromUsers[name]
		if !ok {
			ops = append(ops, EnsureUser{Name: name, Groups: slices.Clone(desired.Groups)})
			continue
		}
		if !isSubset(cur.Groups, desired.Groups) {
			return nil, fmt.Errorf("user %q group removal is not allowed", name)
		}
		if len(missing(cur.Groups, desired.Groups)) > 0 {
			ops = append(ops, EnsureUser{Name: name, Groups: slices.Clone(desired.Groups)})
		}
	}
	// If from has extra users, we cannot reach exact to without deletion.
	for name := range fromUsers {
		if _, ok := toUsers[name]; !ok {
			return nil, fmt.Errorf("user %q removal is not allowed", name)
		}
	}

	// Labels: treat as exact match for now (no safe monotonic semantics defined).
	if !mapsEqual(from.Labels, to.Labels) {
		return nil, fmt.Errorf("labels differ (no monotonic plan defined)")
	}

	return ops, nil
}

func isSubset(a, b []string) bool {
	// Assumes both are normalized sorted unique sets.
	if len(a) == 0 {
		return true
	}
	if len(b) == 0 {
		return false
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			i++
			j++
			continue
		}
		if a[i] > b[j] {
			j++
			continue
		}
		// a[i] < b[j] => missing
		return false
	}
	return i == len(a)
}

func missing(from, to []string) []string {
	// Assumes both are normalized sorted unique sets.
	if len(to) == 0 {
		return nil
	}
	if len(from) == 0 {
		return slices.Clone(to)
	}
	out := make([]string, 0)
	i, j := 0, 0
	for j < len(to) {
		if i >= len(from) {
			out = append(out, to[j:]...)
			break
		}
		if from[i] == to[j] {
			i++
			j++
			continue
		}
		if from[i] > to[j] {
			out = append(out, to[j])
			j++
			continue
		}
		// from[i] < to[j]
		i++
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firewallRuleIDs(in []FirewallRuleSpec) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, r := range in {
		if r.ID != "" {
			out = append(out, r.ID)
		}
	}
	// Already normalized via Normalize(), but keep it safe.
	slices.Sort(out)
	out = slices.Compact(out)
	return out
}

func servicesToMap(in []ServiceSpec) map[string]ServiceState {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ServiceState, len(in))
	for _, s := range in {
		if s.Name == "" {
			continue
		}
		out[s.Name] = s.State
	}
	return out
}

func usersToMap(in []UserSpec) map[string]UserSpec {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]UserSpec, len(in))
	for _, u := range in {
		if u.Name == "" {
			continue
		}
		out[u.Name] = u
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
