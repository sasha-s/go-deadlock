package deadlock

import (
	"testing"
	"time"
)

func TestNormalDeadlockDetection(t *testing.T) {
	oldTimeout := Opts.DeadlockTimeout
	oldOnDeadlock := Opts.OnPotentialDeadlock
	Opts.DeadlockTimeout = 20 * time.Millisecond
	// onDeadlockTimeout calls Opts.OnPotentialDeadlock on a timer goroutine
	// (line 352 of deadlock.go). The channel send inside the callback creates a
	// happens-before edge so the deferred restore below won't race with the read.
	callbackDone := make(chan struct{}, 1)
	Opts.OnPotentialDeadlock = func() {
		t.Log("Deadlock detected!")
		callbackDone <- struct{}{}
	}
	defer func() {
		Opts.DeadlockTimeout = oldTimeout
		Opts.OnPotentialDeadlock = oldOnDeadlock
	}()

	var mu Mutex
	mu.Lock()
	mu.Unlock()

	mu.Lock()

	done := make(chan bool, 1)
	go func() {
		mu.Lock()
		done <- true
		mu.Unlock()
	}()

	time.Sleep(30 * time.Millisecond)
	mu.Unlock()
	<-done
	<-callbackDone
}
