package model

// ResourceTemplate is a reusable desired shape for a resource. It is kept
// separate from Pool because a template describes what should be built while
// a Pool describes how much inventory to maintain.
type ResourceTemplate struct {
	Name     string         `json:"name" yaml:"name"`
	Extends  string         `json:"extends,omitempty" yaml:"extends,omitempty"`
	Type     string         `json:"type,omitempty" yaml:"type,omitempty"`
	Provider string         `json:"provider,omitempty" yaml:"provider,omitempty"`
	Agent    string         `json:"agent,omitempty" yaml:"agent,omitempty"`
	Source   string         `json:"source,omitempty" yaml:"source,omitempty"`
	Config   map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
	Packages []string       `json:"packages,omitempty" yaml:"packages,omitempty"`
}
