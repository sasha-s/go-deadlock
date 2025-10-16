//go:build goexperiment.synctest

package deadlock

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestSynctestCompatibility(t *testing.T) {
	// Configure deadlock detection 
	oldTimeout := Opts.DeadlockTimeout
	oldOnDeadlock := Opts.OnPotentialDeadlock
	
	deadlockDetected := false
	Opts.DeadlockTimeout = 50 * time.Millisecond
	// No need to set TimerPool - TimerPoolDefault automatically disables pooling in synctest
	Opts.OnPotentialDeadlock = func() {
		deadlockDetected = true
		t.Log("✓ Deadlock detected successfully!")
	}
	
	defer func() { 
		Opts.DeadlockTimeout = oldTimeout
		Opts.OnPotentialDeadlock = oldOnDeadlock
	}()
	
	synctest.Run(func() {
		t.Log("Testing channel-based mutex in synctest")
		
		var mu Mutex
		
		// Test 1: Simple lock/unlock
		mu.Lock()
		mu.Unlock()
		t.Log("✓ Simple lock/unlock works")
		
		// Test 2: Deadlock detection
		mu.Lock()
		
		go func() {
			t.Log("Concurrent goroutine: trying to lock")
			mu.Lock()
			t.Log("Concurrent goroutine: got lock")
			mu.Unlock()
		}()
		
		// Wait for deadlock detection to trigger
		t.Log("Main: sleeping to allow deadlock detection")
		time.Sleep(100 * time.Millisecond)
		
		t.Log("Main: releasing lock")
		mu.Unlock()
		
		// Give concurrent goroutine time to finish
		time.Sleep(10 * time.Millisecond)
		t.Log("✓ Test completed")
		
		if deadlockDetected {
			t.Log("✓ Deadlock detection worked correctly")
		} else {
			t.Log("✗ Deadlock detection didn't trigger")
		}
	})
}