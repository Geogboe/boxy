package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Geogboe/boxy/internal/cli"
)

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cli.Version = Version
	cli.GitCommit = GitCommit
	cli.BuildDate = BuildDate

	root := cli.NewRootCommand()
	if err := root.ExecuteContext(ctx); err != nil {
		reportCommandError(os.Stderr, err)
		return exitStatusForError(err)
	}
	return 0
}

func reportCommandError(w io.Writer, err error) {
	if !cli.IsReported(err) {
		_, _ = fmt.Fprintf(w, "Error: %v\n", err)
	}
}

func exitStatusForError(err error) int {
	if code, ok := cli.ExitCode(err); ok {
		return code
	}
	return 1
}
