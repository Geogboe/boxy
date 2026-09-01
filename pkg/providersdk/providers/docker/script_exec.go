package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Geogboe/boxy/pkg/eventstream"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

func dockerScriptInterpreter(requested providersdk.ScriptInterpreter, platform, image string) (providersdk.ScriptInterpreter, error) {
	if requested == "" {
		requested = providersdk.ScriptInterpreterAuto
	}
	if requested == providersdk.ScriptInterpreterAuto {
		switch strings.ToLower(strings.TrimSpace(platform)) {
		case "linux":
			return providersdk.ScriptInterpreterSH, nil
		case "windows":
			return providersdk.ScriptInterpreterPowerShell, nil
		}
		if strings.Contains(strings.ToLower(image), "windows") {
			return providersdk.ScriptInterpreterPowerShell, nil
		}
		return "", errors.New("cannot determine the Docker container script interpreter; specify --interpreter powershell or --interpreter sh")
	}
	return requested, nil
}

func (d *Driver) execScriptInContainer(ctx context.Context, id string, op *ExecOp, sink eventstream.Sink) (*providersdk.Result, error) {
	if op.Script == nil {
		return nil, errors.New("script operation is required")
	}
	if err := op.Script.VerifyDigest(); err != nil {
		return nil, err
	}
	info, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("docker ContainerInspect %s: %w", id, err)
	}
	platform := ""
	image := ""
	if info.ContainerJSONBase != nil {
		platform = info.Platform
	}
	if info.Config != nil {
		image = info.Config.Image
	}
	interpreter, err := dockerScriptInterpreter(op.Script.Interpreter, platform, image)
	if err != nil {
		return nil, err
	}
	path := dockerScriptPath(op.Script.Digest, interpreter)
	probeCmd, probeArgs := dockerScriptProbe(path, interpreter, op.Script.Digest)
	probe, err := d.runDockerExec(ctx, id, container.ExecOptions{Cmd: append([]string{probeCmd}, probeArgs...), AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, fmt.Errorf("probe Docker script cache: %w", err)
	}
	if probe.exitCode != 0 || !strings.EqualFold(strings.TrimSpace(probe.stdout), "hit") {
		if err := d.stageDockerScript(ctx, id, path, interpreter, op.Script.Content); err != nil {
			return nil, err
		}
	}

	command := dockerScriptCommand(path, interpreter, op.Script.Args)
	if sink == nil {
		return d.execInContainer(ctx, id, &ExecOp{Command: command})
	}
	return d.execStreamCommand(ctx, id, command, sink)
}

func dockerScriptPath(digest string, interpreter providersdk.ScriptInterpreter) string {
	ext := "sh"
	if interpreter == providersdk.ScriptInterpreterPowerShell {
		ext = "ps1"
	}
	return "/tmp/boxy-script-cache/" + strings.ToLower(digest) + "." + ext
}

func dockerScriptProbe(path string, interpreter providersdk.ScriptInterpreter, digest string) (string, []string) {
	if interpreter == providersdk.ScriptInterpreterPowerShell {
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf("if ((Test-Path -LiteralPath '%s') -and ((Get-FileHash -LiteralPath '%s' -Algorithm SHA256).Hash -eq '%s')) { 'hit' } else { 'miss' }", path, path, strings.ToUpper(digest))}
	}
	return "sh", []string{"-c", fmt.Sprintf("if [ -f %s ] && [ \"$(sha256sum %s | awk '{print $1}')\" = %s ]; then printf hit; else printf miss; fi", shellQuote(path), shellQuote(path), shellQuote(strings.ToLower(digest)))}
}

func dockerScriptCommand(path string, interpreter providersdk.ScriptInterpreter, args []string) []string {
	if interpreter == providersdk.ScriptInterpreterPowerShell {
		return append([]string{"powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path}, args...)
	}
	return append([]string{"sh", "-c", "exec \"$@\"", "boxy-script", path}, args...)
}

func (d *Driver) stageDockerScript(ctx context.Context, id, path string, interpreter providersdk.ScriptInterpreter, content []byte) error {
	dir := "/tmp/boxy-script-cache"
	tmpPath := path + ".tmp"
	var command []string
	if interpreter == providersdk.ScriptInterpreterPowerShell {
		command = []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf("$dir='%s'; New-Item -ItemType Directory -Path $dir -Force | Out-Null; $tmp='%s.'+[guid]::NewGuid().ToString()+'.tmp'; $input=[Console]::OpenStandardInput(); $output=[IO.File]::OpenWrite($tmp); try { $input.CopyTo($output) } finally { $output.Dispose() }; Move-Item -LiteralPath $tmp -Destination '%s' -Force; $files=@(Get-ChildItem -LiteralPath $dir -File | Sort-Object LastWriteTimeUtc -Descending); if ($files.Count -gt 64) { $files | Select-Object -Skip 64 | Remove-Item -Force }", dir, path, path)}
	} else {
		command = []string{"sh", "-c", fmt.Sprintf("set -eu; dir=%s; mkdir -p \"$dir\"; chmod 700 \"$dir\"; tmp=%s.$$; cat > \"$tmp\"; chmod 700 \"$tmp\"; mv -f \"$tmp\" %s; find \"$dir\" -type f -printf '%%T@ %%p\\n' | sort -nr | awk 'NR>64 {print $2}' | xargs -r rm -f", shellQuote(dir), shellQuote(tmpPath), shellQuote(path))}
	}
	result, err := d.runDockerExecWithStdin(ctx, id, command, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("stage Docker script: %w", err)
	}
	if result.exitCode != 0 {
		return fmt.Errorf("stage Docker script exited with code %d", result.exitCode)
	}
	return nil
}

type dockerExecResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func (d *Driver) runDockerExec(ctx context.Context, id string, opts container.ExecOptions) (dockerExecResult, error) {
	execResp, err := d.cli.ContainerExecCreate(ctx, id, opts)
	if err != nil {
		return dockerExecResult{}, fmt.Errorf("docker ContainerExecCreate %s: %w", id, err)
	}
	attach, err := d.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return dockerExecResult{}, fmt.Errorf("docker ContainerExecAttach %s: %w", execResp.ID, err)
	}
	defer attach.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attach.Reader); err != nil {
		return dockerExecResult{}, fmt.Errorf("docker exec read output: %w", err)
	}
	inspect, err := d.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return dockerExecResult{}, fmt.Errorf("docker ContainerExecInspect %s: %w", execResp.ID, err)
	}
	return dockerExecResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: inspect.ExitCode}, nil
}

func (d *Driver) runDockerExecWithStdin(ctx context.Context, id string, command []string, content io.Reader) (dockerExecResult, error) {
	execResp, err := d.cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: command, AttachStdin: true, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return dockerExecResult{}, fmt.Errorf("docker ContainerExecCreate %s: %w", id, err)
	}
	attach, err := d.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return dockerExecResult{}, fmt.Errorf("docker ContainerExecAttach %s: %w", execResp.ID, err)
	}
	defer attach.Close()
	if _, err := io.Copy(attach.Conn, content); err != nil {
		return dockerExecResult{}, fmt.Errorf("write Docker script: %w", err)
	}
	if err := attach.CloseWrite(); err != nil {
		return dockerExecResult{}, fmt.Errorf("close Docker script input: %w", err)
	}
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attach.Reader); err != nil {
		return dockerExecResult{}, fmt.Errorf("read staged Docker script: %w", err)
	}
	inspect, err := d.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return dockerExecResult{}, fmt.Errorf("docker ContainerExecInspect %s: %w", execResp.ID, err)
	}
	return dockerExecResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: inspect.ExitCode}, nil
}

func (d *Driver) execStreamCommand(ctx context.Context, id string, command []string, sink eventstream.Sink) (*providersdk.Result, error) {
	execResp, err := d.cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: command, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, fmt.Errorf("docker ContainerExecCreate %s: %w", id, err)
	}
	attach, err := d.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker ContainerExecAttach %s: %w", execResp.ID, err)
	}
	defer attach.Close()
	stdout := streamWriter{ctx: ctx, sink: sink, channel: eventstream.Channel("stdout")}
	stderr := streamWriter{ctx: ctx, sink: sink, channel: eventstream.Channel("stderr")}
	if _, err := stdcopy.StdCopy(stdout, stderr, attach.Reader); err != nil {
		return nil, fmt.Errorf("docker exec stream output: %w", err)
	}
	inspect, err := d.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return nil, fmt.Errorf("docker ContainerExecInspect %s: %w", execResp.ID, err)
	}
	return &providersdk.Result{Outputs: map[string]string{"exit_code": fmt.Sprint(inspect.ExitCode)}}, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
