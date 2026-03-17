// Package resourcepool provides a small, reusable, homogeneous collection type
// with an attached (opaque) policy.
//
// This package is intentionally "dumb":
// - It enforces homogeneity (all items share the same key).
// - It supports safe removal (Take) for single-use allocation patterns.
// - It does not evaluate policy or perform IO; higher-level code does that.
package resourcepool
