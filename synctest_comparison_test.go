package deadlock

import (
	"testing"
	"time"
)

func TestNormalDeadlockDetection(t *testing.T) {
	oldTimeout := Opts.DeadlockTimeout
	oldOnDeadlock := Opts.OnPotentialDeadlock
	Opts.DeadlockTimeout = 20 * time.Millisecond
	Opts.OnPotentialDeadlock = func() {
		t.Log("Deadlock detected!")
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
}
