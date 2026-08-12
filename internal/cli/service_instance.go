// internal/cli/service_instance.go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// instanceNamePattern restricts --instance-name to characters safe across
// every service backend it ends up embedded in: a Windows SCM service name
// or Task Scheduler task name, a systemd unit name, a launchd label, and a
// filesystem directory name (the default data/state directory gets the same
// suffix — see instanceDirSuffix). Letters, digits, and interior hyphens
// only, starting with a letter or digit.
var instanceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// maxInstanceNameLen keeps the resolved service name (base + "-" + instance)
// comfortably under every backend's real limit (Windows service names allow
// up to 256 characters, systemd unit names up to 255 bytes) with generous
// headroom — this is about keeping names typeable and greppable, not
// approaching any actual OS ceiling.
const maxInstanceNameLen = 32

// validateInstanceName rejects an --instance-name value that could produce
// an invalid service/unit/task name or an unexpected filesystem path. Empty
// is always valid — it selects the default (unnamed) instance, preserving
// existing single-instance behavior and its fixed "boxy-agent"/"boxy-serve"
// names.
func validateInstanceName(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > maxInstanceNameLen {
		return fmt.Errorf("--instance-name %q is too long (max %d characters)", name, maxInstanceNameLen)
	}
	if !instanceNamePattern.MatchString(name) {
		return fmt.Errorf("--instance-name %q is invalid: must start with a letter or digit and contain only letters, digits, and hyphens", name)
	}
	return nil
}

// serviceInstanceName returns base unchanged when instanceName is empty
// (the default instance keeps its existing fixed name — "boxy-agent" or
// "boxy-serve" — so upgrading boxy does not orphan an already-installed
// service), or "base-instanceName" for a named instance.
func serviceInstanceName(base, instanceName string) string {
	if instanceName == "" {
		return base
	}
	return base + "-" + instanceName
}

// instanceDirSuffix mirrors serviceInstanceName for default data/state
// directory names, so named instances don't collide with the default
// instance's directory (or each other) without the caller having to also
// remember to pass a distinct --data-dir/--boxy-dir every time.
func instanceDirSuffix(instanceName string) string {
	if instanceName == "" {
		return ""
	}
	return "-" + instanceName
}

// purgeServiceDataDir removes dir only if it looks like a boxy-managed
// service data directory — i.e. it contains service.yaml. --purge must
// never blindly rm -rf a path just because it was computed from a name:
// service names are host-global but data directories are cwd-relative, so
// an --instance-name that doesn't match the directory the service was
// actually installed from (wrong cwd, explicit --data-dir override, a
// forgotten --instance-name) must not silently delete an unrelated
// directory that happens to exist at the computed path. A missing
// directory is treated as already-purged, not an error.
func purgeServiceDataDir(dir string) error {
	marker := filepath.Join(dir, "service.yaml")
	if _, err := os.Stat(marker); err != nil {
		if os.IsNotExist(err) {
			if _, dirErr := os.Stat(dir); os.IsNotExist(dirErr) {
				return nil // already gone — nothing to purge
			}
			return fmt.Errorf("refusing to purge %q: no service.yaml found there — this doesn't look like a boxy service data directory (check --instance-name and --data-dir/--boxy-dir)", dir)
		}
		return fmt.Errorf("check %q: %w", marker, err)
	}
	return os.RemoveAll(dir)
}
