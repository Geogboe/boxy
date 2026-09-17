package cli

import (
	"context"
	"testing"
	"time"
)

func TestReconcileWakeupCoalescesAndRetainsWorkDuringPass(t *testing.T) {
	wake, notify := newReconcileWakeup()
	for range 1000 {
		notify()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runReconcileLoop(ctx, wake, nil, func() { entered <- struct{}{}; <-release })
	}()
	await := func() {
		t.Helper()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("notification did not start a pass")
		}
	}
	await()
	for range 1000 {
		notify()
	}
	release <- struct{}{}
	await()
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not stop")
	}
	select {
	case <-entered:
		t.Fatal("extra pass after cancellation")
	default:
	}
}

func TestReconcileWakeupRetainsPeriodicRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	passes := 0
	runReconcileLoop(ctx, nil, ticks, func() { passes++; cancel() })
	if passes != 1 {
		t.Fatalf("passes = %d", passes)
	}
}
