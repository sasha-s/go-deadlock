package deadlock

import (
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoDeadlocks(t *testing.T) {
	defer restore()()
	Opts.DeadlockTimeout = time.Millisecond * 5000
	var a RWMutex
	var b Mutex
	var c RWMutex
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 5; k++ {
				func() {
					a.Lock()
					defer a.Unlock()
					func() {
						b.Lock()
						defer b.Unlock()
						func() {
							c.RLock()
							defer c.RUnlock()
							time.Sleep(time.Duration((1000 + rand.Intn(1000))) * time.Millisecond / 200)
						}()
					}()
				}()
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 5; k++ {
				func() {
					a.RLock()
					defer a.RUnlock()
					func() {
						b.Lock()
						defer b.Unlock()
						func() {
							c.Lock()
							defer c.Unlock()
							time.Sleep(time.Duration((1000 + rand.Intn(1000))) * time.Millisecond / 200)
						}()
					}()
				}()
			}
		}()
	}
	wg.Wait()
}

func TestLockOrder(t *testing.T) {
	defer restore()()
	Opts.DeadlockTimeout = 0
	var deadlocks uint32
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
	}
	var a RWMutex
	var b Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.Lock()
		b.Lock()
		b.Unlock()
		a.Unlock()
	}()
	wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.Lock()
		a.RLock()
		a.RUnlock()
		b.Unlock()
	}()
	wg.Wait()
	if atomic.LoadUint32(&deadlocks) != 1 {
		t.Fatalf("expected 1 deadlock, detected %d", deadlocks)
	}
}

func TestHardDeadlock(t *testing.T) {
	defer restore()()
	Opts.DisableLockOrderDetection = true
	Opts.DeadlockTimeout = time.Millisecond * 20
	var deadlocks uint32
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
	}
	var mu Mutex
	mu.Lock()
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		mu.Lock()
		defer mu.Unlock()
	}()
	select {
	case <-ch:
	case <-time.After(time.Millisecond * 100):
	}
	if atomic.LoadUint32(&deadlocks) != 1 {
		t.Fatalf("expected 1 deadlock, detected %d", deadlocks)
	}
	log.Println("****************")
	mu.Unlock()
	<-ch
}

func TestRWMutex(t *testing.T) {
	defer restore()()
	Opts.DeadlockTimeout = time.Millisecond * 20
	var deadlocks uint32
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
	}
	var a RWMutex
	a.RLock()
	go func() {
		// We detect a potential deadlock here.
		a.Lock()
		defer a.Unlock()
	}()
	time.Sleep(time.Millisecond * 100) // We want the Lock call to happen.
	ch := make(chan struct{})
	go func() {
		// We detect a potential deadlock here.
		defer close(ch)
		a.RLock()
		defer a.RUnlock()
	}()
	select {
	case <-ch:
		t.Fatal("expected a timeout")
	case <-time.After(time.Millisecond * 50):
	}
	a.RUnlock()
	if atomic.LoadUint32(&deadlocks) != 2 {
		t.Fatalf("expected 2 deadlocks, detected %d", deadlocks)
	}
	<-ch
}

func restore() func() {
	opts := Opts
	return func() {
		Opts = opts
	}
}

func TestStarvedRLockMultipleReaders(t *testing.T) {
	defer restore()()
	Opts.DisableLockOrderDetection = true
	Opts.DeadlockTimeout = time.Millisecond * 20
	var deadlocks uint32
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
	}
	var a RWMutex

	// Reader 1 holds RLock for the duration of the test.
	a.RLock()

	// Reader 2 briefly holds and releases RLock. Before the fix, this
	// corrupted the cur map: postLock overwrote reader 1's entry, then
	// postUnlock deleted it entirely even though reader 1 still held it.
	done := make(chan struct{})
	go func() {
		a.RLock()
		a.RUnlock()
		close(done)
	}()
	<-done

	// Writer tries to Lock — blocks because reader 1 still holds RLock.
	go func() {
		a.Lock()
		defer a.Unlock()
	}()
	time.Sleep(time.Millisecond * 100)

	// Starved reader tries RLock — blocked by the pending writer.
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		a.RLock()
		defer a.RUnlock()
	}()
	select {
	case <-ch:
		t.Fatal("expected a timeout")
	case <-time.After(time.Millisecond * 100):
	}

	if atomic.LoadUint32(&deadlocks) != 2 {
		t.Fatalf("expected 2 deadlocks, detected %d", deadlocks)
	}
	a.RUnlock()
	<-ch
}

// TestManyReadersFewWriters stresses the RWMutex tracking under high read
// concurrency with infrequent writers. Existing tests use at most ~10
// goroutines with a balanced reader/writer mix; real-world usage often has
// dozens of readers racing against a handful of writers. This exercises:
//   - the per-goroutine cur map ref-counting under heavy concurrent RLock/RUnlock,
//     where many goroutines simultaneously call postLock and postUnlock;
//   - lock-order detection with a large number of concurrent reader entries;
//   - timer pool contention when many DeadlockTimeout timers are live at once.
func TestManyReadersFewWriters(t *testing.T) {
	defer restore()()
	Opts.DeadlockTimeout = time.Millisecond * 5000
	var mu RWMutex
	var wg sync.WaitGroup

	const numReaders = 100
	const numWriters = 3
	const readerIters = 50
	const writerIters = 10

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < readerIters; k++ {
				mu.RLock()
				time.Sleep(time.Duration(rand.Intn(500)) * time.Microsecond)
				mu.RUnlock()
			}
		}()
	}

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < writerIters; k++ {
				mu.Lock()
				time.Sleep(time.Duration(rand.Intn(200)) * time.Microsecond)
				mu.Unlock()
				time.Sleep(time.Duration(rand.Intn(1000)) * time.Microsecond)
			}
		}()
	}

	wg.Wait()
}

// TestConcurrentLockOrderDetection verifies that lock-order violation detection
// works correctly under real goroutine contention. TestLockOrder runs its two
// goroutines sequentially (wg.Wait() between them), so the order map and cur map
// are only contested by one goroutine at a time. Here, many goroutines
// simultaneously call preLock, postLock, and postUnlock — all contending on
// lo.mu — while each one independently detects the same A→B vs B→A conflict.
// This stresses concurrent iteration of lo.cur, concurrent reads/writes to
// lo.order, and concurrent invocations of OnPotentialDeadlock.
func TestConcurrentLockOrderDetection(t *testing.T) {
	defer restore()()
	Opts.DeadlockTimeout = 0
	var deadlocks uint32
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
	}

	var a, b Mutex

	// Establish the A→B ordering in the lock-order map.
	a.Lock()
	b.Lock()
	b.Unlock()
	a.Unlock()

	// Launch many goroutines that all acquire B→A concurrently. Each one
	// triggers a violation in preLock when it tries to acquire A while holding
	// B. Because every goroutine acquires in the same order (B then A), they
	// cannot actually deadlock with each other.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 10; k++ {
				b.Lock()
				a.Lock()
				a.Unlock()
				b.Unlock()
			}
		}()
	}

	close(start)
	wg.Wait()

	if d := atomic.LoadUint32(&deadlocks); d == 0 {
		t.Fatal("expected at least 1 lock-order violation, detected 0")
	}
}

// TestDeadlockTimeoutTransientNoHolder exercises the reschedule path in
// onDeadlockTimeout. When the deadlock timer fires and lo.cur has no holder
// for the waited-on lock — a transient state that can occur between the
// previous holder's postUnlock and the waiter's deregister — the callback
// reschedules itself via time.AfterFunc instead of reporting a false deadlock.
//
// In production this window is extremely narrow (nanoseconds between
// postUnlock removing the holder from lo.cur and the waiter's lockFn
// returning), so it cannot be triggered reliably through normal Lock/Unlock
// calls. Instead we invoke onDeadlockTimeout directly with a pendingEntry
// whose lock has no holders in lo.cur, deterministically hitting the
// reschedule branch. We then set done=1 (simulating the waiter acquiring the
// lock) and verify the rescheduled timer fires harmlessly with no false
// deadlock report.
func TestDeadlockTimeoutTransientNoHolder(t *testing.T) {
	defer restore()()
	Opts.DisableLockOrderDetection = true
	Opts.DeadlockTimeout = 10 * time.Millisecond
	var deadlocks uint32
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
	}

	var mu Mutex

	// Build a pendingEntry as if a goroutine were blocked waiting on mu.
	// mu has never been locked, so lo.cur has no holders for it.
	e := newPendingEntry()
	e.ptr = &mu
	e.gid = 999
	atomic.StoreInt32(&e.done, 0)

	// Directly invoke the timeout handler. It will find no holders in lo.cur
	// and take the reschedule branch (time.AfterFunc) instead of reporting.
	onDeadlockTimeout(e)

	// Simulate the waiter acquiring the lock. The rescheduled timer's checkFn
	// will see done=1 and no-op.
	atomic.StoreInt32(&e.done, 1)

	// Wait long enough for the rescheduled timer to fire and confirm it
	// does not report a false deadlock.
	time.Sleep(Opts.DeadlockTimeout * 3)

	if d := atomic.LoadUint32(&deadlocks); d != 0 {
		t.Fatalf("expected 0 false deadlocks from transient no-holder state, got %d", d)
	}
}

func TestLockDuplicate(t *testing.T) {
	// No restore() here: the goroutines below permanently block inside lockFn
	// after preLock detects recursion. A deferred restore() would write to Opts
	// while those goroutines are still reading Opts.DeadlockTimeout, causing a
	// data race under -race. Omitting restore is safe because every other test
	// saves/sets its own Opts via restore().
	Opts.DeadlockTimeout = 0
	var deadlocks uint32
	detected := make(chan struct{}, 2)
	Opts.OnPotentialDeadlock = func() {
		atomic.AddUint32(&deadlocks, 1)
		detected <- struct{}{}
	}
	var a RWMutex
	var b Mutex
	go func() {
		a.RLock()
		a.Lock()
		a.RUnlock()
		a.Unlock()
	}()
	go func() {
		b.Lock()
		b.Lock()
		b.Unlock()
		b.Unlock()
	}()
	<-detected
	<-detected
	if atomic.LoadUint32(&deadlocks) != 2 {
		t.Fatalf("expected 2 deadlocks, detected %d", deadlocks)
	}
}

// TestOrderViolationCallbackPooledStackRace demonstrates a data race introduced
// by PR #55's order-violation path: preLock unlocks lo.mu before calling
// OnPotentialDeadlock and re-acquires it after, but then continues to read
// bs.stack — a slice into a pooled stack buffer — and copyStack() it into
// lo.order. During the unlock window, a concurrent cross-goroutine Unlock can
// release that exact buffer back to stackBufPool, where another goroutine grabs
// it from the pool and writes to it. The subsequent copyStack(bs.stack) then
// races with that write.
//
// Run with `go test -race -run TestOrderViolationCallbackPooledStackRace` to
// observe the data race report.
func TestOrderViolationCallbackPooledStackRace(t *testing.T) {
	defer restore()()
	Opts.DeadlockTimeout = 0
	Opts.OnPotentialDeadlock = func() {} // placeholder, overridden below

	var a, b Mutex

	// Prime lo.order with the "a then b" ordering.
	a.Lock()
	b.Lock()
	b.Unlock()
	a.Unlock()

	// Hold b in this goroutine, then snapshot the exact stack buffer that
	// preLock will later read through bs.stack. We close over this pointer in
	// the helper to write to it concurrently with preLock's copyStack().
	b.Lock()

	lo.mu.Lock()
	if len(lo.cur[&b]) != 1 {
		lo.mu.Unlock()
		t.Fatalf("expected exactly 1 holder for b, got %d", len(lo.cur[&b]))
	}
	holderBuf := lo.cur[&b][0].buf
	lo.mu.Unlock()

	if holderBuf == nil {
		t.Fatal("expected holder buf to be non-nil")
	}

	callbackEntered := make(chan struct{})
	helperDone := make(chan struct{})

	Opts.OnPotentialDeadlock = func() {
		// At this point in the post-PR code, lo.mu has already been released.
		// Signal the helper to start writing to the pooled buffer.
		close(callbackEntered)
		// Give the helper time to start churning so its writes overlap with
		// preLock's copyStack(bs.stack) after this callback returns and lo.mu
		// is re-acquired.
		time.Sleep(50 * time.Millisecond)
	}

	go func() {
		defer close(helperDone)
		<-callbackEntered
		// Tight write loop directly on the same backing array that preLock's
		// bs.stack points into. preLock will re-acquire lo.mu after the
		// callback and execute copyStack(bs.stack), which calls
		// copy(c, bs.stack) — a read of this exact memory. Since the writes
		// here have no happens-before with that read (no shared mutex, no
		// channel), the race detector will flag the read.
		for i := 0; i < 5000000; i++ {
			holderBuf[0] = uintptr(i)
		}
	}()

	// Trigger preLock's order-violation path. Post-PR this calls the callback,
	// re-acquires lo.mu, and runs copyStack(bs.stack) — racing the helper.
	a.Lock()
	a.Unlock()

	<-helperDone

	// b is still locked (we never unlocked it). Release it for cleanup.
	b.Unlock()
}
