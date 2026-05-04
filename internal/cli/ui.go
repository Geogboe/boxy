package cli

import (
	"fmt"
	"os"

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

	spin, _ := boxySpinner.Start(label)
	done = func(detail string) {
		if detail != "" {
			spin.Success(label + "  " + pterm.FgDarkGray.Sprint(detail))
		} else {
			spin.Success(label)
		}
	}
	fail = func(detail string) {
		if detail != "" {
			spin.Fail(label + "  \u2014 " + detail)
		} else {
			spin.Fail(label)
		}
	}
	return done, fail
}

func useSpinnerOutput() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // Fd() fits in int on all supported platforms.
}
