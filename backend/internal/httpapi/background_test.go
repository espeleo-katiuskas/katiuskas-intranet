package httpapi

import (
	"sync/atomic"
	"testing"
	"time"
)

// WaitBackground returns only once the work started with Go has finished.
func TestWaitBackgroundWaitsForWhatGoStarted(t *testing.T) {
	var finished atomic.Bool
	Go(func() {
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
	})
	WaitBackground()
	if !finished.Load() {
		t.Error("WaitBackground returned before the work was over")
	}
	// With nothing running it returns at once.
	done := make(chan struct{})
	go func() {
		WaitBackground()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("WaitBackground did not return with nothing to wait for")
	}
}
