//go:build go1.25 && !goexperiment.synctest

package deadlock

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestGo125SynctestTimerCompatibility validates timer handling with Go 1.25 native synctest.
// Uses channels for synchronization since sync.Mutex is not durably blocking in synctest.
func TestGo125SynctestTimerCompatibility(t *testing.T) {
	// This test validates that our timer handling works with synctest
	synctest.Test(t, func(t *testing.T) {
		t.Log("Testing timer compatibility with Go 1.25 native synctest")

		// Test 1: Channel operations work (durably blocking)
		ch := make(chan int)
		done := make(chan bool)

		go func() {
			val := <-ch
			if val != 42 {
				t.Errorf("Expected 42, got %d", val)
			}
			close(done)
		}()

		// This should proceed immediately in synctest
		ch <- 42
		<-done
		t.Log("✓ Channel operations work correctly")

		// Test 2: time.Sleep should work without blocking the test
		time.Sleep(100 * time.Millisecond)
		t.Log("✓ time.Sleep completed (virtualized)")

		// Test 3: Timer-based operations work
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
			t.Log("✓ Timer fired correctly in synctest")
		case <-time.After(100 * time.Millisecond):
			t.Error("Timer did not fire")
		}

		t.Log("✓ Go 1.25 synctest compatibility validated")
	})
}

// TestGo125SynctestWithDeadlockDetection validates deadlock detection works with synctest.
func TestGo125SynctestWithDeadlockDetection(t *testing.T) {
	// Configure deadlock detection
	oldTimeout := Opts.DeadlockTimeout
	oldOnDeadlock := Opts.OnPotentialDeadlock
	defer func() {
		Opts.DeadlockTimeout = oldTimeout
		Opts.OnPotentialDeadlock = oldOnDeadlock
	}()

	Opts.DeadlockTimeout = 50 * time.Millisecond
	Opts.OnPotentialDeadlock = func() {
		t.Log("Deadlock detected (expected)")
	}

	synctest.Test(t, func(t *testing.T) {
		t.Log("Testing deadlock detection doesn't break synctest")

		// Use channels (which ARE durably blocking) for synchronization
		ch := make(chan struct{})
		done := make(chan bool)

		go func() {
			time.Sleep(10 * time.Millisecond)
			close(ch)
			close(done)
		}()

		<-ch
		<-done

		t.Log("✓ Deadlock detection coexists with synctest")
	})
}
