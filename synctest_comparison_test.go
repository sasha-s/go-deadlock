package deadlock

import (
	"testing"
	"time"
)

// clearTimerPool drains all timers from the pool
func clearTimerPool() {
	// Keep getting timers until the pool returns nil
	for {
		obj := timersPool.Get()
		if obj == nil {
			break
		}
		// Don't try to stop the timers - just discard them
		// Stopping them would trigger "stop of synctest timer from outside bubble"
		// if they were created inside a synctest bubble
	}
}

func TestNormalDeadlockDetection(t *testing.T) {
	// Clear the timer pool
	clearTimerPool()

	// Configure deadlock detection - same as synctest version
	oldTimeout := Opts.DeadlockTimeout
	oldOnDeadlock := Opts.OnPotentialDeadlock
	Opts.DeadlockTimeout = 20 * time.Millisecond // Shorter timeout
	Opts.OnPotentialDeadlock = func() {
		t.Log("Deadlock detected!")
		// Don't exit, just log
	}
	defer func() {
		Opts.DeadlockTimeout = oldTimeout
		Opts.OnPotentialDeadlock = oldOnDeadlock
	}()

	// Same test logic as synctest version, but without synctest.Run
	t.Log("Starting normal test")

	// Simple test - just lock and unlock
	var mu Mutex
	t.Log("About to acquire first lock")
	mu.Lock()
	t.Log("First lock acquired")
	mu.Unlock()
	t.Log("First lock released - simple test succeeded")

	// Test with concurrent lock attempt
	t.Log("About to start concurrent test")
	mu.Lock()
	t.Log("Main goroutine has lock, starting concurrent goroutine")

	done := make(chan bool, 1)
	go func() {
		t.Log("Concurrent goroutine: attempting to acquire lock")
		// This will trigger deadlock detection
		mu.Lock()
		t.Log("Concurrent goroutine: lock acquired")
		done <- true
		mu.Unlock()
		t.Log("Concurrent goroutine: lock released")
	}()

	t.Log("Main goroutine: sleeping to allow deadlock detection")
	// Give deadlock detection time to trigger
	time.Sleep(30 * time.Millisecond)
	t.Log("Main goroutine: finished sleeping")

	t.Log("Main goroutine: releasing lock")
	mu.Unlock()

	t.Log("Main goroutine: waiting for concurrent goroutine to finish")
	<-done
	t.Log("Main goroutine: concurrent goroutine finished")

	// Clear pool again after test
	clearTimerPool()
}
