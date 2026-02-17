// Package resourcespec defines a comparable, provider-agnostic resource
// customization spec and pure planning helpers.
//
// Intent:
//   - Provide a stable "shape" for declarative customizations that can be compared.
//   - Enable planning monotonic upgrades (add packages, add firewall rules, enable services)
//     while rejecting risky/non-monotonic transitions (removals, disabling services, etc).
//
// This package is deliberately pure: no provider IO, no state inspection, no hooks.
package resourcespec
