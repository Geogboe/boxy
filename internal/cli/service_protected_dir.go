// internal/cli/service_protected_dir.go
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// protectedServiceInstallGOOS is checked instead of runtime.GOOS directly
// everywhere in this file, so tests can exercise the Windows-only staging
// logic on any host.
var protectedServiceInstallGOOS = runtime.GOOS

// protectedServiceRoot returns %ProgramFiles%\Boxy in production;
// overridable in tests (via protectedServiceRootFn) so they never touch
// the real Program Files directory.
var protectedServiceRootFn = func() string {
	return filepath.Join(os.Getenv("ProgramFiles"), "Boxy")
}

// wintunDLLName is the file this package looks for alongside a boxy
// executable to also stage into a protected service directory -- what
// scripts/install.ps1 copies next to boxy.exe when a release archive
// includes one (#379 blocker 6).
const wintunDLLName = "wintun.dll"

// usesProtectedServiceDir reports whether a service install should stage a
// protected copy of the binary rather than pointing the service straight
// at srcExePath.
//
// Only a real (non --user) Windows install qualifies. Installing a real
// Windows Service already requires elevation, and the exposure this closes
// -- a privileged service loading a binary/DLL from a location the
// interactively-logged-in user can overwrite -- doesn't apply to a --user
// install (an unprivileged Task Scheduler task run as that same user has
// no privilege boundary to protect) or to Linux/macOS system units (a
// distinct, not-yet-addressed exposure -- see AGENTS.md's Installer Notes
// on the data-dir/service.yaml ACL gap, which this also doesn't close).
func usesProtectedServiceDir(userMode bool) bool {
	return protectedServiceInstallGOOS == "windows" && !userMode
}

// protectedServiceDir returns the per-service-instance directory a real
// Windows service install stages its own binary copy into --
// %ProgramFiles%\Boxy\<svcName>\.
//
// Scoped per svcName (one directory per installed service, not one shared
// directory for every boxy service on the host) for three reasons:
// installing a second service can't hit a sharing violation trying to
// overwrite a first service's already-running exe; agent and serve
// installs can never end up serving skewed versions of a binary they
// happen to share; and uninstall can delete its own directory outright
// without any risk to another still-installed service's copy.
func protectedServiceDir(svcName string) string {
	return filepath.Join(protectedServiceRootFn(), svcName)
}

// stageProtectedServiceBinary copies srcExePath, and -- if present
// alongside it -- wintun.dll, into protectedServiceDir(svcName), creating
// the directory as needed. Returns the copied binary's own path, to use as
// the service's ExecPath in place of srcExePath.
//
// No custom ACL/icacls hardening is applied here: %ProgramFiles%'s default
// ACL inheritance already restricts write access to Administrators/SYSTEM
// on a stock Windows install -- that inherited restriction is precisely
// why Windows software installs there instead of a user-writable
// AppData/home directory, and re-deriving the same guarantee by hand would
// only add a new, untested way to get it wrong. Installing to this
// location is the hardening.
//
// Callers must stop any service currently running from an existing copy
// at this destination before calling this on a refresh path (see
// restartInstalledDefaultServices in update.go) -- copyFile below does not
// attempt to overwrite a locked, running executable's file, and will
// return whatever error the OS gives if the destination is still in use.
func stageProtectedServiceBinary(svcName, srcExePath string) (string, error) {
	dir := protectedServiceDir(svcName)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // %ProgramFiles%'s inherited ACLs already restrict write access; this directory is not itself a secret
		return "", fmt.Errorf("create protected service directory %q: %w", dir, err)
	}

	dstExePath := filepath.Join(dir, filepath.Base(srcExePath))
	if err := copyFile(srcExePath, dstExePath); err != nil {
		return "", fmt.Errorf("stage service binary into %q: %w", dir, err)
	}

	srcWintun := filepath.Join(filepath.Dir(srcExePath), wintunDLLName)
	if _, err := os.Stat(srcWintun); err == nil {
		if err := copyFile(srcWintun, filepath.Join(dir, wintunDLLName)); err != nil {
			return "", fmt.Errorf("stage %s into %q: %w", wintunDLLName, dir, err)
		}
	}

	return dstExePath, nil
}

// copyFile copies src to dst, unless they're already the same path (a
// re-run pointed straight at an already-protected install has nothing to
// copy, and copying a file onto itself would at best no-op and at worst
// truncate it depending on open-mode ordering).
//
// Writes to a .tmp sibling first and renames into place, so a failure or
// interruption mid-copy never leaves a half-written binary at dst -- the
// existing file (if any) stays exactly as it was until the new one is
// verified complete.
func copyFile(src, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil
	}
	in, err := os.Open(src) //nolint:gosec // src is always this process's own resolved executable path or a wintun.dll found beside it, never caller-supplied
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755) //nolint:gosec // an installed service binary/DLL is executed, not secret; 0o755 matches how it will be read+executed regardless
	if err != nil {
		return fmt.Errorf("create %q: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %q: %w", tmp, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q to %q: %w", tmp, dst, err)
	}
	return nil
}

// removeProtectedServiceDir deletes protectedServiceDir(svcName) and
// everything in it. Idempotent -- a directory that was never created (no
// protected copy was ever staged for svcName, e.g. it was only ever
// installed --user) is not an error, matching this codebase's
// Delete/Destroy idempotency convention elsewhere.
func removeProtectedServiceDir(svcName string) error {
	if err := os.RemoveAll(protectedServiceDir(svcName)); err != nil {
		return fmt.Errorf("remove protected service directory for %q: %w", svcName, err)
	}
	return nil
}
