// Package policycontroller provides a small, reusable Observe → Decide → Act loop.
//
// It is intentionally generic: domain packages supply concrete Observer, Evaluator,
// and Actuator implementations (inventory, pools, sandboxes, etc).
package policycontroller
