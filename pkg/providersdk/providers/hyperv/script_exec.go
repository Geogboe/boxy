package hyperv

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/Geogboe/boxy/pkg/vmsdk"
)

func scriptInterpreterForGuest(requested providersdk.ScriptInterpreter, guestOS string) (providersdk.ScriptInterpreter, error) {
	if requested == "" {
		requested = providersdk.ScriptInterpreterAuto
	}
	if requested == providersdk.ScriptInterpreterAuto {
		switch strings.ToLower(strings.TrimSpace(guestOS)) {
		case "windows":
			return providersdk.ScriptInterpreterPowerShell, nil
		case "linux":
			return providersdk.ScriptInterpreterSH, nil
		default:
			return "", fmt.Errorf("cannot determine the guest script interpreter; specify --interpreter powershell or --interpreter sh")
		}
	}
	switch requested {
	case providersdk.ScriptInterpreterPowerShell:
		if !strings.EqualFold(guestOS, "windows") {
			return "", fmt.Errorf("PowerShell scripts require a Windows guest; specify --interpreter sh for a Linux guest")
		}
	case providersdk.ScriptInterpreterSH:
		if !strings.EqualFold(guestOS, "linux") {
			return "", fmt.Errorf("sh scripts require a Linux guest; specify --interpreter powershell for a Windows guest")
		}
	default:
		return "", fmt.Errorf("unsupported script interpreter %q", requested)
	}
	return requested, nil
}

func stageHyperVScript(ctx context.Context, ge vmsdk.GuestExec, script *providersdk.ScriptSpec, guestOS string) (string, error) {
	if err := script.VerifyDigest(); err != nil {
		return "", err
	}
	interpreter, err := scriptInterpreterForGuest(script.Interpreter, guestOS)
	if err != nil {
		return "", err
	}
	digest := strings.ToLower(script.Digest)
	if interpreter == providersdk.ScriptInterpreterPowerShell {
		path := fmt.Sprintf("$env:TEMP\\boxy-script-cache\\%s.ps1", digest)
		probe := fmt.Sprintf("$d='%s'; $p=Join-Path $env:TEMP ('boxy-script-cache\\'+$d+'.ps1'); if ((Test-Path -LiteralPath $p) -and ((Get-FileHash -LiteralPath $p -Algorithm SHA256).Hash -eq $d)) { 'hit' } else { 'miss' }", psq(digest))
		result, err := ge.Exec(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", probe)
		if err != nil {
			return "", fmt.Errorf("probe PowerShell script cache: %w", err)
		}
		if strings.EqualFold(strings.TrimSpace(result.Stdout), "hit") {
			return path, nil
		}
		tmp, err := randHex(8)
		if err != nil {
			return "", err
		}
		encoded := base64.StdEncoding.EncodeToString(script.Content)
		stage := fmt.Sprintf("$d='%s'; $dir=Join-Path $env:TEMP 'boxy-script-cache'; New-Item -ItemType Directory -Path $dir -Force | Out-Null; $tmp=Join-Path $dir ('%s.'+'%s.tmp'); [IO.File]::WriteAllBytes($tmp,[Convert]::FromBase64String('%s')); Move-Item -LiteralPath $tmp -Destination (Join-Path $dir ($d+'.ps1')) -Force; $files=@(Get-ChildItem -LiteralPath $dir -File | Sort-Object LastWriteTimeUtc -Descending); if ($files.Count -gt 64) { $files | Select-Object -Skip 64 | Remove-Item -Force }", psq(digest), digest, tmp, encoded)
		if result, err := ge.Exec(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", stage); err != nil || result == nil || result.ExitCode != 0 {
			if err != nil {
				return "", fmt.Errorf("stage PowerShell script: %w", err)
			}
			return "", fmt.Errorf("stage PowerShell script exited with code %d", result.ExitCode)
		}
		return path, nil
	}

	path := "/tmp/boxy-script-cache/" + digest + ".sh"
	probe := fmt.Sprintf("dir=/tmp/boxy-script-cache; p=%s; if [ -f \"$p\" ] && [ \"$(sha256sum \"$p\" | awk '{print $1}')\" = %s ]; then printf hit; else printf miss; fi", shellQuote(path), shellQuote(digest))
	result, err := ge.Exec(ctx, "sh", "-c", probe)
	if err != nil {
		return "", fmt.Errorf("probe shell script cache: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(result.Stdout), "hit") {
		return path, nil
	}
	tmp, err := randHex(8)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(script.Content)
	stage := fmt.Sprintf("set -eu; dir=/tmp/boxy-script-cache; mkdir -p \"$dir\"; chmod 700 \"$dir\"; tmp=\"$dir/%s.%s.tmp\"; printf %%s %s | base64 -d > \"$tmp\"; chmod 700 \"$tmp\"; mv -f \"$tmp\" %s; find \"$dir\" -type f -name '*.sh' -printf '%%T@ %%p\\n' | sort -nr | awk 'NR>64 {print $2}' | xargs -r rm -f", digest, tmp, shellQuote(encoded), shellQuote(path))
	if result, err := ge.Exec(ctx, "sh", "-c", stage); err != nil || result == nil || result.ExitCode != 0 {
		if err != nil {
			return "", fmt.Errorf("stage shell script: %w", err)
		}
		return "", fmt.Errorf("stage shell script exited with code %d", result.ExitCode)
	}
	return path, nil
}

func hyperVScriptCommand(interpreter providersdk.ScriptInterpreter, path string, args []string) (string, []string) {
	if interpreter == providersdk.ScriptInterpreterPowerShell {
		command := "$p=Join-Path $env:TEMP 'boxy-script-cache\\" + strings.TrimPrefix(path, "$env:TEMP\\boxy-script-cache\\") + "'; & $p"
		for _, arg := range args {
			command += " '" + psq(arg) + "'"
		}
		return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command}
	}
	return "sh", append([]string{"-c", "exec " + shellQuote(path) + " \"$@\"", "boxy-script"}, args...)
}
