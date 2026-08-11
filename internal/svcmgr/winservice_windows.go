//go:build windows

package svcmgr

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows/svc"
)

// RunAsWindowsService checks whether the current process was launched by
// the Service Control Manager. If not, it returns (false, nil) so the
// caller proceeds with a normal foreground run. If so, it drives the
// svc.Handler protocol itself: run(ctx) executes in a goroutine, and an
// SCM stop/shutdown request cancels ctx and waits for run to return before
// reporting Stopped.
func RunAsWindowsService(name string, run func(ctx context.Context) error) (bool, error) {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("determine windows service session: %w", err)
	}
	if !isSvc {
		return false, nil
	}

	h := &winServiceHandler{run: run}
	if err := svc.Run(name, h); err != nil {
		return true, fmt.Errorf("run windows service %q: %w", name, err)
	}
	return true, h.runErr
}

type winServiceHandler struct {
	run    func(ctx context.Context) error
	runErr error
}

func (h *winServiceHandler) Execute(_ []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	s <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.runErr = h.run(ctx)
		close(done)
	}()

	s <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case <-done:
			s <- svc.Status{State: svc.Stopped}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				s <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				s <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				s <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}
