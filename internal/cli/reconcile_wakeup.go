package cli

import (
	"context"
	"time"
)

// A wakeup is a hint to inspect durable state, not one queued task per event.
// Capacity one bounds bursts and retains a notification received during a pass.
func newReconcileWakeup() (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	return wake, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func runReconcileLoop(ctx context.Context, wake <-chan struct{}, ticks <-chan time.Time, pass func()) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticks:
		}
		if ctx.Err() == nil {
			pass()
		}
	}
}
