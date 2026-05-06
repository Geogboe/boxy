package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pterm/pterm"
	"golang.org/x/term"
)

var boxySuccessPrinter = pterm.PrefixPrinter{
	MessageStyle: &pterm.ThemeDefault.SuccessMessageStyle,
	Prefix:       pterm.Prefix{Text: "  ✓", Style: pterm.NewStyle(pterm.FgGreen)},
}

var boxyFailPrinter = pterm.PrefixPrinter{
	MessageStyle: &pterm.ThemeDefault.ErrorMessageStyle,
	Prefix:       pterm.Prefix{Text: "  ✗", Style: pterm.NewStyle(pterm.FgRed)},
}

// boxySpinner is the shared spinner style for all Boxy CLI commands.
// It replaces pterm's heavy "SUCCESS/FAIL" badge labels with simple
// Unicode tick/cross marks, and hides the elapsed timer during spinning.
var boxySpinner = pterm.SpinnerPrinter{
	Sequence:            []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Style:               pterm.NewStyle(pterm.FgCyan),
	Delay:               pterm.DefaultSpinner.Delay,
	MessageStyle:        pterm.NewStyle(pterm.FgDefault),
	SuccessPrinter:      &boxySuccessPrinter,
	FailPrinter:         &boxyFailPrinter,
	ShowTimer:           false,
	TimerRoundingFactor: pterm.DefaultSpinner.TimerRoundingFactor,
	TimerStyle:          pterm.DefaultSpinner.TimerStyle,
	Writer:              pterm.DefaultSpinner.Writer,
}

// step starts a boxySpinner for a single CLI step and returns a done callback
// that marks it successful and a fail callback that marks it failed.
// Both accept an optional detail string appended to the label.
func step(label string) (done func(detail string), fail func(detail string)) {
	if !useSpinnerOutput() {
		done = func(detail string) {
			if detail != "" {
				boxySuccessPrinter.Println(fmt.Sprintf("%s  %s", label, detail))
				return
			}
			boxySuccessPrinter.Println(label)
		}
		fail = func(detail string) {
			if detail != "" {
				boxyFailPrinter.Println(label + "  \u2014 " + detail)
				return
			}
			boxyFailPrinter.Println(label)
		}
		return done, fail
	}

	// Create a SpinnerPrinter value copy without calling Start(). pterm's Start()
	// launches a goroutine that reads IsActive without synchronisation; calling
	// Stop()/Fail()/Success() concurrently with that goroutine is a data race.
	// Instead we manage our own animation goroutine and stop it (joining via a
	// channel) before delegating to the pterm finalisation methods so that
	// IsActive is never written while a goroutine is reading it.
	sp := boxySpinner // value copy – captures Writer, Style, etc. at call time
	sp.IsActive = true
	sp.Text = label

	delay := sp.Delay
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}

	stopCh := make(chan struct{})
	stoppedCh := make(chan struct{})

	go func() {
		defer close(stoppedCh)
		ticker := time.NewTicker(delay)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if !pterm.RawOutput {
					seq := sp.Sequence[i%len(sp.Sequence)]
					i++
					pterm.Fprinto(sp.Writer, sp.Style.Sprint(seq)+" "+sp.MessageStyle.Sprint(sp.Text))
				}
			}
		}
	}()

	var once sync.Once
	finalize := func(fn func()) {
		once.Do(func() {
			close(stopCh)
			<-stoppedCh // wait for animation goroutine to exit before touching sp
			fn()
		})
	}

	done = func(detail string) {
		finalize(func() {
			if detail != "" {
				sp.Success(label + "  " + pterm.FgDarkGray.Sprint(detail))
			} else {
				sp.Success(label)
			}
		})
	}
	fail = func(detail string) {
		finalize(func() {
			if detail != "" {
				sp.Fail(label + "  \u2014 " + detail)
			} else {
				sp.Fail(label)
			}
		})
	}
	return done, fail
}

// useSpinnerOutput reports whether interactive spinner output should be used.
// It is a variable so tests can override it to exercise the spinner code path.
var useSpinnerOutput = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // Fd() fits in int on all supported platforms.
}
