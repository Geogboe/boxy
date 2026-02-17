package process

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Geogboe/boxy/v2/pkg/providersdk"
)

// Driver implements providersdk.Driver and providersdk.ExecCapability for local process execution.
type Driver struct {
	// MaxOutputBytes caps stdout/stderr capture to prevent unbounded memory growth.
	// Defaults to 1 MiB if unset.
	MaxOutputBytes int
}

func New() *Driver { return &Driver{} }

func (*Driver) Type() providersdk.Type { return "process" }

func (*Driver) ValidateConfig(ctx context.Context, inst providersdk.Instance) error {
	_ = ctx
	_ = inst
	// No config required for local process execution.
	return nil
}

func (d *Driver) Exec(ctx context.Context, inst providersdk.Instance, target providersdk.Target, spec providersdk.ExecSpec) (providersdk.ExecResult, error) {
	_ = inst
	_ = target

	if len(spec.Command) == 0 || strings.TrimSpace(spec.Command[0]) == "" {
		return providersdk.ExecResult{}, fmt.Errorf("exec.command is required")
	}

	maxOut := d.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = 1 << 20
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := exec.CommandContext(execCtx, spec.Command[0], spec.Command[1:]...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}

	cmd.Env = mergeEnv(os.Environ(), spec.Env)

	var stdoutBuf, stderrBuf limitedBuffer
	stdoutBuf.limit = maxOut
	stderrBuf.limit = maxOut
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()

	res := providersdk.ExecResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}

	if execCtx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("exec timeout after %s", spec.Timeout)
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]struct{}, len(overrides))
	for k := range overrides {
		seen[k] = struct{}{}
	}
	for _, kv := range base {
		k, _, ok := strings.Cut(kv, "=")
		if ok {
			if _, override := seen[k]; override {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

type limitedBuffer struct {
	limit int
	buf   bytes.Buffer
	n     int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	remain := b.limit - b.n
	if remain <= 0 {
		// Pretend we consumed bytes to keep callers happy; discard.
		b.n += len(p)
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = b.buf.Write(p[:remain])
		b.n += len(p)
		return len(p), nil
	}
	n, err := b.buf.Write(p)
	b.n += n
	return n, err
}

func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.limit > 0 && b.n > b.limit {
		s += "\n<output truncated>\n"
	}
	return s
}
