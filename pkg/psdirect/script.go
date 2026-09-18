package psdirect

import (
	"context"
	"fmt"

	"github.com/Geogboe/boxy/pkg/vmsdk"
)

const scriptCompletion = "boxy:provider-script:completed"

// ExecScript executes trusted provider source in the existing runspace. Its
// arguments are PSRP objects, not a native command line. Script failures are
// intentionally reported without error records that could contain credentials.
func (s *Session) ExecScript(ctx context.Context, script string, args ...string) (*vmsdk.ExecResult, error) {
	values := make([]interface{}, len(args))
	for i, arg := range args {
		values[i] = arg
	}
	wrapped := "$ErrorActionPreference = 'Stop'\ntry {\n& {\n" + script +
		"\n} @args\n'" + scriptCompletion + "'\n} catch { throw 'provider script failed' }"
	result, err := s.executor.ExecuteCommand(ctx, wrapped, true, values...)
	if err != nil {
		return nil, fmt.Errorf("psdirect: provider script on VM %s: %w", s.vmID, wrapKnownTransportError(err))
	}
	if result == nil || result.HadErrors || len(result.Errors) != 0 || len(result.Output) == 0 {
		return nil, fmt.Errorf("psdirect: provider script on VM %s did not complete successfully", s.vmID)
	}
	last, ok := result.Output[len(result.Output)-1].(string)
	if !ok || last != scriptCompletion {
		return nil, fmt.Errorf("psdirect: provider script on VM %s missing completion", s.vmID)
	}
	// Append an explicit zero so a script's final integer output is not
	// mistaken for a native exit code by the shared output formatter.
	output := result.Output[:len(result.Output)-1]
	output = append(output, int32(0))
	stdout, _ := extractOutput(output)
	return &vmsdk.ExecResult{Stdout: stdout}, nil
}
