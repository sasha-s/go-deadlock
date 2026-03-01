package deadlock

// HeavyStruct is used by GC-related tests to verify that lock order tracking
// does not prevent garbage collection of structs containing deadlock.Mutex.
type HeavyStruct struct {
	Mutex
	data [1024 * 1024]byte
}
