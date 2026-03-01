//go:build go1.24

package deadlock

import (
	"runtime"
	"testing"
)

func TestOrderMapGC(t *testing.T) {
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
	t.Logf("HeapInuse: %d MB", ms.HeapInuse/(1024*1024))
	if ms.HeapInuse > 20*1024*1024 {
		t.Errorf("Memory leak: HeapInuse = %d MB, expected < 20 MB. Lock order map retains references.", ms.HeapInuse/(1024*1024))
	}
}
