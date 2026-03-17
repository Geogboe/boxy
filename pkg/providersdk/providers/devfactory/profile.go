package devfactory

import (
	"strconv"
	"time"
)

// Profile determines what kind of resource a devfactory resource simulates.
// Each profile produces different connection info and default latency
// to mimic the behavior of a real provider type.
type Profile string

const (
	ProfileContainer Profile = "container"
	ProfileVM        Profile = "vm"
	ProfileShare     Profile = "share"
)

// profileSpec defines the simulated characteristics of a profile.
type profileSpec struct {
	// DefaultLatency is used when the config doesn't set an explicit latency.
	DefaultLatency time.Duration
	// ConnInfo generates connection info for a resource given a unique port number.
	ConnInfo func(port int) map[string]string
}

var profiles = map[Profile]profileSpec{
	ProfileContainer: {
		DefaultLatency: 0,
		ConnInfo: func(port int) map[string]string {
			return map[string]string{
				"type": "container",
				"host": "10.0.0." + strconv.Itoa(port%256),
				"port": strconv.Itoa(port),
			}
		},
	},
	ProfileVM: {
		DefaultLatency: 2 * time.Second,
		ConnInfo: func(port int) map[string]string {
			return map[string]string{
				"type":     "vm",
				"host":     "10.1.0." + strconv.Itoa(port%256),
				"ssh_port": "22",
				"ssh_user": "admin",
				"ssh_key":  "/tmp/devfactory/id_ed25519_" + strconv.Itoa(port),
			}
		},
	},
	ProfileShare: {
		DefaultLatency: 0,
		ConnInfo: func(port int) map[string]string {
			return map[string]string{
				"type":       "share",
				"unc_path":   `\\10.2.0.1\share-` + strconv.Itoa(port),
				"mount_path": "/mnt/share-" + strconv.Itoa(port),
				"username":   "svc_boxy",
				"password":   "simulated-credential-" + strconv.Itoa(port),
			}
		},
	},
}

// resolveProfile returns the profileSpec for a given profile name.
// Defaults to container if the profile is empty or unknown.
func resolveProfile(p Profile) profileSpec {
	if spec, ok := profiles[p]; ok {
		return spec
	}
	return profiles[ProfileContainer]
}
