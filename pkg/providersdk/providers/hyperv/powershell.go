package hyperv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// runPS runs a PowerShell 5.1 script using powershell.exe.
// Returns (stdout, error). Stderr is folded into the error with friendly
// Hyper-V-specific messaging.
func runPS(ctx context.Context, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-Command", script,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		return stdout.String(), fmt.Errorf("%s", friendlyErr(err, msg))
	}
	return stdout.String(), nil
}

// friendlyErr translates PowerShell subprocess errors into actionable messages.
func friendlyErr(err error, stderr string) string {
	if errors.Is(err, exec.ErrNotFound) {
		return "powershell.exe not found — Hyper-V management requires Windows PowerShell 5.1"
	}
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "hyper-v") && (strings.Contains(lower, "not installed") || strings.Contains(lower, "not enabled")) {
		return "Hyper-V is not installed or not enabled; enable via 'Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V': " + stderr
	}
	if strings.Contains(lower, "access is denied") || strings.Contains(lower, "accessdenied") {
		return "access denied — run the boxy agent as Administrator for Hyper-V management: " + stderr
	}
	if strings.Contains(lower, "already exists") {
		return "resource already exists: " + stderr
	}
	if stderr != "" {
		return stderr
	}
	return err.Error()
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("randHex: %w", err)
	}
	return hex.EncodeToString(b), nil
}
