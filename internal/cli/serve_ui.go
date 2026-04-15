package cli

import (
	"log/slog"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/pterm/pterm"
)

// serveUI abstracts terminal output for the serve command.
// Spinners always display on the terminal; reconcile errors and shutdown
// messages go to pterm in pretty mode (no log file) or slog in machine mode
// (log file set, so structured output flows there instead).
type serveUI struct {
	pretty bool // true = no log file; pterm is the sole output channel
}

func newServeUI(pretty bool) *serveUI {
	return &serveUI{pretty: pretty}
}

// step starts a spinner for a startup step using the shared boxySpinner style.
// The returned function marks the step successful and appends a detail string.
func (u *serveUI) step(label string) func(detail string) {
	return step(label)
}

// reconcileError reports a pool reconciliation error.
func (u *serveUI) reconcileError(pool model.PoolName, err error) {
	if u.pretty {
		pterm.Error.Printfln("[pool=%s] %v", pool, err)
	} else {
		slog.Error("reconcile pool", "pool", pool, "err", err)
	}
}

// printErr reports a non-pool-specific reconciliation error.
func (u *serveUI) printErr(err error) {
	if u.pretty {
		pterm.Error.Printfln("%v", err)
	} else {
		slog.Error("reconcile sandboxes", "err", err)
	}
}

// shutdown prints the shutdown message.
func (u *serveUI) shutdown() {
	if u.pretty {
		pterm.Info.Println("Shutting down...")
	} else {
		slog.Info("shutting down")
	}
}
