//go:build !go1.24

package deadlock

import (
	"runtime"
	"testing"
)

// TestOrderMapGCLegacy documents that on Go < 1.24 the lock order map retains
// strong references to mutexes, preventing GC. This is a known limitation
// fixed by weak pointers available in Go 1.24+.
func TestOrderMapGCLegacy(t *testing.T) {
	Opts.DeadlockTimeout = 0
	Opts.OnPotentialDeadlock = func() {}
	defer func() {
		Opts.DeadlockTimeout = 30e9
		Opts.OnPotentialDeadlock = func() { panic("deadlock") }
	}()

	var anchor Mutex
	for i := 0; i < 50; i++ {
		h := &HeavyStruct{}
		h.data[0] = byte(i)
		anchor.Lock()
		h.Lock()
		h.Unlock()
		anchor.Unlock()
	}

	runtime.GC()
	runtime.GC()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("HeapInuse: %d MB (known leak on Go < 1.24)", ms.HeapInuse/(1024*1024))
}
