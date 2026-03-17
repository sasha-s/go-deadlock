package deadlock

import (
	"testing"
)

func BenchmarkRegisterDeregister(b *testing.B) {
	for i := 0; i < b.N; i++ {
		id := dw.register(nil, nil, 0, Opts.DeadlockTimeout)
		dw.deregister(id)
	}
}

func BenchmarkLockUnlock(b *testing.B) {
	var mu Mutex
	for i := 0; i < b.N; i++ {
		mu.Lock()
		mu.Unlock()
	}
}
