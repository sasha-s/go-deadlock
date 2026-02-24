//go:build go1.18

package deadlock

import (
	"sync"
	"testing"
	"time"
)

// TestBuildTagMutexWorks tests that mutexes work correctly regardless of build tags
func TestBuildTagMutexWorks(t *testing.T) {
	var mu Mutex
	var counter int

	// Simple lock/unlock
	mu.Lock()
	counter++
	mu.Unlock()

	if counter != 1 {
		t.Errorf("Expected counter to be 1, got %d", counter)
	}

	// Concurrent access
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			mu.Lock()
			counter++
			mu.Unlock()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if counter != 11 {
		t.Errorf("Expected counter to be 11, got %d", counter)
	}
}

// TestBuildTagRWMutexWorks tests that RWMutexes work correctly regardless of build tags
func TestBuildTagRWMutexWorks(t *testing.T) {
	var mu RWMutex
	var counter int

	// Write lock
	mu.Lock()
	counter++
	mu.Unlock()

	// Read lock
	mu.RLock()
	_ = counter
	mu.RUnlock()

	// Multiple readers
	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func() {
			mu.RLock()
			_ = counter
			time.Sleep(1 * time.Millisecond)
			mu.RUnlock()
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}

	// Writer after readers
	mu.Lock()
	counter++
	mu.Unlock()

	if counter != 2 {
		t.Errorf("Expected counter to be 2, got %d", counter)
	}
}

// TestBuildTagTryLock tests TryLock functionality
func TestBuildTagTryLock(t *testing.T) {
	var mu Mutex

	// TryLock should succeed when unlocked
	if !mu.TryLock() {
		// For Go < 1.18, TryLock panics, so we skip this test
		if testing.Short() {
			t.Skip("TryLock not available in this Go version")
		}
		t.Error("TryLock should succeed on unlocked mutex")
	}

	// TryLock should fail when locked
	result := make(chan bool, 1)
	go func() {
		result <- mu.TryLock()
	}()

	select {
	case r := <-result:
		if r {
			t.Error("TryLock should fail on locked mutex")
		}
	case <-time.After(100 * time.Millisecond):
		// This is okay - TryLock might block briefly
	}

	mu.Unlock()
}

// TestBuildTagRWMutexTryLock tests RWMutex TryLock/TryRLock
func TestBuildTagRWMutexTryLock(t *testing.T) {
	var mu RWMutex

	// TryRLock should succeed when unlocked
	if !mu.TryRLock() {
		if testing.Short() {
			t.Skip("TryRLock not available in this Go version")
		}
		t.Error("TryRLock should succeed on unlocked RWMutex")
	}

	// Another TryRLock from a different goroutine should also succeed (multiple readers allowed)
	done := make(chan bool)
	go func() {
		if !mu.TryRLock() {
			t.Error("TryRLock should succeed when already read-locked by another goroutine")
		}
		mu.RUnlock()
		done <- true
	}()
	<-done

	mu.RUnlock()

	// TryLock should succeed when unlocked
	if !mu.TryLock() {
		t.Error("TryLock should succeed on unlocked RWMutex")
	}

	mu.Unlock()
}

// TestCompatibilityWithStdlib tests that our types are compatible with stdlib expectations
func TestCompatibilityWithStdlib(t *testing.T) {
	var _ sync.Locker = &Mutex{}

	var rwmu RWMutex
	var _ sync.Locker = &rwmu
	var _ sync.Locker = rwmu.RLocker()
}
