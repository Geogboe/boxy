package agent

import (
	"fmt"
	"slices"
)

// Policy decides whether a task is allowed to run locally.
type Policy interface {
	Allow(info Info, task Task) error
}

// DefaultPolicy enforces selectors and required capabilities.
type DefaultPolicy struct{}

func (DefaultPolicy) Allow(info Info, task Task) error {
	if task.ID == "" {
		return fmt.Errorf("task id is required")
	}
	if !MatchesSelector(info.Labels, task.Selector) {
		return fmt.Errorf("task selector does not match agent labels")
	}
	if !HasAll(info.Capabilities, task.RequiredCapabilities) {
		return fmt.Errorf("task required capabilities not satisfied")
	}
	if err := validateAction(task.Action); err != nil {
		return err
	}
	return nil
}

func validateAction(a Action) error {
	if a.Type == "" {
		return fmt.Errorf("action type is required")
	}
	switch a.Type {
	case ActionTypeExec:
		if a.Exec == nil {
			return fmt.Errorf("exec action is required")
		}
		if len(a.Exec.Command) == 0 {
			return fmt.Errorf("exec.command is required")
		}
	default:
		return fmt.Errorf("unsupported action type %q", a.Type)
	}
	return nil
}

// MatchesSelector returns true if all selector kv pairs exist with identical values in labels.
func MatchesSelector(labels map[string]string, selector Selector) bool {
	if len(selector) == 0 {
		return true
	}
	if len(labels) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// HasAll returns true if sup contains all required entries (set semantics).
func HasAll(sup []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	if len(sup) == 0 {
		return false
	}
	for _, r := range required {
		if !slices.Contains(sup, r) {
			return false
		}
	}
	return true
}
