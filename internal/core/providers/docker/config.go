package docker

// Config is the typed configuration for a docker Provider.Type.
//
// This is the code source-of-truth for how Boxy interprets Provider.Config for
// docker providers. JSON Schema exists for editor UX; runtime validation happens
// via this struct + Validate.
type Config struct {
	// Host is the docker daemon address.
	// Examples: unix:///var/run/docker.sock, tcp://docker-host:2376
	Host string `json:"host"`

	// Context is a docker context name (alternative to Host).
	Context string `json:"context"`

	// Namespace is a prefix/namespace for names/labels Boxy creates.
	Namespace string `json:"namespace"`

	// DefaultLabels are labels applied to all created resources.
	DefaultLabels map[string]string `json:"default_labels"`

	TLS TLSConfig `json:"tls"`
}

type TLSConfig struct {
	Enabled            bool   `json:"enabled"`
	CAFile             string `json:"ca_file"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}
