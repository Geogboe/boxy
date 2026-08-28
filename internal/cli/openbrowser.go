package cli

import (
	"os/exec"
	"runtime"
)

// openBrowser best-effort launches the system's default browser at url.
// Kept internal rather than pulled in as a dependency (see
// docs/superpowers/specs/2026-08-28-oidc-ui-and-cli-auth-design.md's
// Decision 6): Go's stdlib has no "open URL in browser" primitive, and a
// per-OS exec.Command call is small enough not to justify one. url is
// always our own constructed authorization URL, never attacker-controlled
// input, and exec.Command never invokes a shell, so there is no injection
// concern in passing it as an argument.
var openBrowser = func(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
