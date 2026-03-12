package deadlock

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/petermattis/goid"
)

// TimerPoolMode controls timer pooling behavior
type TimerPoolMode int

const (
	// TimerPoolDefault automatically chooses based on build environment
	TimerPoolDefault TimerPoolMode = iota
	// TimerPoolEnabled always uses timer pooling for performance
	TimerPoolEnabled
	// TimerPoolDisabled disables timer pooling (required for testing/synctest)
	TimerPoolDisabled
)

// Opts control how deadlock detection behaves.
// Options are supposed to be set once at a startup (say, when parsing flags).
var Opts = struct {
	// Mutex/RWMutex would work exactly as their sync counterparts
	// -- almost no runtime penalty, no deadlock detection if Disable == true.
	Disable bool
	// Would disable lock order based deadlock detection if DisableLockOrderDetection == true.
	DisableLockOrderDetection bool
	// Waiting for a lock for longer than DeadlockTimeout is considered a deadlock.
	// Ignored if DeadlockTimeout <= 0.
	DeadlockTimeout time.Duration
	// OnPotentialDeadlock is called each time a potential deadlock is detected -- either based on
	// lock order or on lock wait time.
	OnPotentialDeadlock func()
	// Will keep MaxMapSize lock pairs (happens before // happens after) in the map.
	// The map resets once the threshold is reached.
	MaxMapSize int
	// Will dump stacktraces of all goroutines when inconsistent locking is detected.
	PrintAllCurrentGoroutines bool
	// Controls timer pooling behavior.
	// TimerPoolDefault: Automatically choose based on build environment
	// TimerPoolEnabled: Always use timer pooling
	// TimerPoolDisabled: Never use timer pooling
	TimerPool TimerPoolMode
	mu        *sync.Mutex // Protects the LogBuf.
	// Will print deadlock info to log buffer.
	LogBuf io.Writer
}{
	DeadlockTimeout: time.Second * 30,
	OnPotentialDeadlock: func() {
		os.Exit(2)
	},
	MaxMapSize: 1024 * 64,
	mu:         &sync.Mutex{},
	LogBuf:     os.Stderr,
}

// Cond is sync.Cond wrapper
type Cond struct {
	sync.Cond
}

// Locker is sync.Locker wrapper
type Locker struct {
	sync.Locker
}

// Once is sync.Once wrapper
type Once struct {
	sync.Once
}

// Pool is sync.Poll wrapper
type Pool struct {
	sync.Pool
}

// WaitGroup is sync.WaitGroup wrapper
type WaitGroup struct {
	sync.WaitGroup
}

// NewCond is a sync.NewCond wrapper
var NewCond = sync.NewCond

// A Mutex is a drop-in replacement for sync.Mutex.
// Performs deadlock detection unless disabled in Opts.
type Mutex struct {
	mu StandardMutex
}

// Lock locks the mutex.
// If the lock is already in use, the calling goroutine
// blocks until the mutex is available.
//
// Unless deadlock detection is disabled, logs potential deadlocks to Opts.LogBuf,
// calling Opts.OnPotentialDeadlock on each occasion.
func (m *Mutex) Lock() {
	lock(m.mu.Lock, m)
}

// Unlock unlocks the mutex.
// It is a run-time error if m is not locked on entry to Unlock.
//
// A locked Mutex is not associated with a particular goroutine.
// It is allowed for one goroutine to lock a Mutex and then
// arrange for another goroutine to unlock it.
func (m *Mutex) Unlock() {
	m.mu.Unlock()
	if !Opts.Disable {
		postUnlock(m)
	}
}

// An RWMutex is a drop-in replacement for sync.RWMutex.
// Performs deadlock detection unless disabled in Opts.
type RWMutex struct {
	mu StandardRWMutex
}

// Lock locks rw for writing.
// If the lock is already locked for reading or writing,
// Lock blocks until the lock is available.
// To ensure that the lock eventually becomes available,
// a blocked Lock call excludes new readers from acquiring
// the lock.
//
// Unless deadlock detection is disabled, logs potential deadlocks to Opts.LogBuf,
// calling Opts.OnPotentialDeadlock on each occasion.
func (m *RWMutex) Lock() {
	lock(m.mu.Lock, m)
}

// Unlock unlocks the mutex for writing.  It is a run-time error if rw is
// not locked for writing on entry to Unlock.
//
// As with Mutexes, a locked RWMutex is not associated with a particular
// goroutine.  One goroutine may RLock (Lock) an RWMutex and then
// arrange for another goroutine to RUnlock (Unlock) it.
func (m *RWMutex) Unlock() {
	m.mu.Unlock()
	if !Opts.Disable {
		postUnlock(m)
	}
}

// RLock locks the mutex for reading.
//
// Unless deadlock detection is disabled, logs potential deadlocks to Opts.LogBuf,
// calling Opts.OnPotentialDeadlock on each occasion.
func (m *RWMutex) RLock() {
	lock(m.mu.RLock, m)
}

// RUnlock undoes a single RLock call;
// it does not affect other simultaneous readers.
// It is a run-time error if rw is not locked for reading
// on entry to RUnlock.
func (m *RWMutex) RUnlock() {
	m.mu.RUnlock()
	if !Opts.Disable {
		postUnlock(m)
	}
}

// RLocker returns a Locker interface that implements
// the Lock and Unlock methods by calling RLock and RUnlock.
func (m *RWMutex) RLocker() sync.Locker {
	return m.mu.RLocker()
}

func preLock(stack []uintptr, p interface{}) {
	lo.preLock(stack, p)
}

func postLock(stack []uintptr, p interface{}) {
	lo.postLock(stack, p)
}

func postUnlock(p interface{}) {
	lo.postUnlock(p)
}

func lock(lockFn func(), ptr interface{}) {
	if Opts.Disable {
		lockFn()
		return
	}
	stack := callers(1)
	preLock(stack, ptr)
	if Opts.DeadlockTimeout <= 0 {
		lockFn()
	} else {
		currentID := goid.Get()
		e := dw.register(stack, ptr, currentID)
		lockFn()
		dw.deregister(e)
		postLock(stack, ptr)
		return
	}
	postLock(stack, ptr)
}

type pendingEntry struct {
	stack   []uintptr
	ptr     interface{}
	gid     int64
	done    int32 // atomic: 0=pending, 1=acquired
	timer   *time.Timer
	checkFn func()
}

func newPendingEntry() *pendingEntry {
	e := &pendingEntry{}
	e.checkFn = func() {
		if atomic.LoadInt32(&e.done) != 0 {
			return
		}
		onDeadlockTimeout(e)
	}
	return e
}

var pendingPool = sync.Pool{
	New: func() interface{} {
		return newPendingEntry()
	},
}

type deadlockWatcher struct{}

var dw deadlockWatcher

func (w *deadlockWatcher) register(stack []uintptr, ptr interface{}, gid int64) *pendingEntry {
	var e *pendingEntry
	if shouldDisableTimerPool() {
		e = newPendingEntry()
	} else {
		e = pendingPool.Get().(*pendingEntry)
	}
	e.stack = stack
	e.ptr = ptr
	e.gid = gid
	atomic.StoreInt32(&e.done, 0)
	if e.timer == nil {
		e.timer = time.AfterFunc(Opts.DeadlockTimeout, e.checkFn)
	} else {
		e.timer.Reset(Opts.DeadlockTimeout)
	}
	return e
}

func (w *deadlockWatcher) deregister(e *pendingEntry) {
	atomic.StoreInt32(&e.done, 1)
	stopped := e.timer.Stop()
	if stopped && !shouldDisableTimerPool() {
		e.stack = nil
		e.ptr = nil
		pendingPool.Put(e)
	}
}

func onDeadlockTimeout(e *pendingEntry) {
	lo.mu.Lock()
	holders, ok := lo.cur[e.ptr]
	if !ok || len(holders) == 0 {
		lo.mu.Unlock()
		if atomic.LoadInt32(&e.done) == 0 {
			time.AfterFunc(Opts.DeadlockTimeout, e.checkFn)
		}
		return
	}
	Opts.mu.Lock()
	fmt.Fprintln(Opts.LogBuf, header)
	for _, prev := range holders {
		fmt.Fprintln(Opts.LogBuf, "Previous place where the lock was grabbed")
		fmt.Fprintf(Opts.LogBuf, "goroutine %v lock %p\n", prev.gid, e.ptr)
		printStack(Opts.LogBuf, prev.stack)
	}
	fmt.Fprintln(Opts.LogBuf, "Have been trying to lock it again for more than", Opts.DeadlockTimeout)
	fmt.Fprintf(Opts.LogBuf, "goroutine %v lock %p\n", e.gid, e.ptr)
	printStack(Opts.LogBuf, e.stack)
	stacks := stacks()
	grs := bytes.Split(stacks, []byte("\n\n"))
	for _, prev := range holders {
		for _, g := range grs {
			if goid.ExtractGID(g) == prev.gid {
				fmt.Fprintln(Opts.LogBuf, "Here is what goroutine", prev.gid, "doing now")
				Opts.LogBuf.Write(g)
				fmt.Fprintln(Opts.LogBuf)
			}
		}
	}
	lo.other(e.ptr)
	if Opts.PrintAllCurrentGoroutines {
		fmt.Fprintln(Opts.LogBuf, "All current goroutines:")
		Opts.LogBuf.Write(stacks)
	}
	fmt.Fprintln(Opts.LogBuf)
	if buf, ok := Opts.LogBuf.(*bufio.Writer); ok {
		buf.Flush()
	}
	Opts.mu.Unlock()
	lo.mu.Unlock()
	Opts.OnPotentialDeadlock()
}

type lockOrder struct {
	mu    sync.Mutex
	cur   map[interface{}][]stackGID // stacktraces + gids for the locks currently taken.
	order map[beforeAfter]ss         // expected order of locks.
}

type stackGID struct {
	stack []uintptr
	gid   int64
}

type ss struct {
	before []uintptr
	after  []uintptr
}

var lo = newLockOrder()

func newLockOrder() *lockOrder {
	return &lockOrder{
		cur:   map[interface{}][]stackGID{},
		order: map[beforeAfter]ss{},
	}
}

func (l *lockOrder) postLock(stack []uintptr, p interface{}) {
	gid := goid.Get()
	l.mu.Lock()
	l.cur[p] = append(l.cur[p], stackGID{stack, gid})
	l.mu.Unlock()
}

func (l *lockOrder) preLock(stack []uintptr, p interface{}) {
	if Opts.DisableLockOrderDetection {
		return
	}
	gid := goid.Get()
	l.mu.Lock()
	for b, holders := range l.cur {
		if b == p {
			for _, bs := range holders {
				if bs.gid == gid {
					Opts.mu.Lock()
					fmt.Fprintln(Opts.LogBuf, header, "Recursive locking:")
					fmt.Fprintf(Opts.LogBuf, "current goroutine %d lock %p\n", gid, b)
					printStack(Opts.LogBuf, stack)
					fmt.Fprintln(Opts.LogBuf, "Previous place where the lock was grabbed (same goroutine)")
					printStack(Opts.LogBuf, bs.stack)
					l.other(p)
					if buf, ok := Opts.LogBuf.(*bufio.Writer); ok {
						buf.Flush()
					}
					Opts.mu.Unlock()
					Opts.OnPotentialDeadlock()
					break
				}
			}
			continue
		}
		for _, bs := range holders {
			if bs.gid != gid { // We want locks taken in the same goroutine only.
				continue
			}
			if s, ok := l.order[newBeforeAfter(p, b)]; ok {
				Opts.mu.Lock()
				fmt.Fprintln(Opts.LogBuf, header, "Inconsistent locking. saw this ordering in one goroutine:")
				fmt.Fprintln(Opts.LogBuf, "happened before")
				printStack(Opts.LogBuf, s.before)
				fmt.Fprintln(Opts.LogBuf, "happened after")
				printStack(Opts.LogBuf, s.after)
				fmt.Fprintln(Opts.LogBuf, "in another goroutine: happened before")
				printStack(Opts.LogBuf, bs.stack)
				fmt.Fprintln(Opts.LogBuf, "happened after")
				printStack(Opts.LogBuf, stack)
				l.other(p)
				fmt.Fprintln(Opts.LogBuf)
				if buf, ok := Opts.LogBuf.(*bufio.Writer); ok {
					buf.Flush()
				}
				Opts.mu.Unlock()
				Opts.OnPotentialDeadlock()
			}
			l.order[newBeforeAfter(b, p)] = ss{bs.stack, stack}
			if len(l.order) == Opts.MaxMapSize { // Reset the map to keep memory footprint bounded.
				l.order = map[beforeAfter]ss{}
			}
		}
	}
	l.mu.Unlock()
}

func (l *lockOrder) postUnlock(p interface{}) {
	gid := goid.Get()
	l.mu.Lock()
	holders := l.cur[p]
	idx := -1
	for i, h := range holders {
		if h.gid == gid {
			idx = i
			break
		}
	}
	if idx >= 0 {
		holders[idx] = holders[len(holders)-1]
		holders[len(holders)-1] = stackGID{} // Zero to avoid retaining stack slice in underlying array.
		holders = holders[:len(holders)-1]
	} else if len(holders) > 0 {
		// Cross-goroutine unlock: no matching gid found, remove an arbitrary entry.
		holders[len(holders)-1] = stackGID{} // Zero to avoid retaining stack slice in underlying array.
		holders = holders[:len(holders)-1]
	}
	if len(holders) == 0 {
		delete(l.cur, p)
	} else {
		l.cur[p] = holders
	}
	l.mu.Unlock()
}

// Under lo.mu Locked.
func (l *lockOrder) other(ptr interface{}) {
	empty := true
	for k, holders := range l.cur {
		if k == ptr {
			continue
		}
		if len(holders) > 0 {
			empty = false
			break
		}
	}
	if empty {
		return
	}
	fmt.Fprintln(Opts.LogBuf, "Other goroutines holding locks:")
	for k, holders := range l.cur {
		if k == ptr {
			continue
		}
		for _, pp := range holders {
			fmt.Fprintf(Opts.LogBuf, "goroutine %v lock %p\n", pp.gid, k)
			printStack(Opts.LogBuf, pp.stack)
		}
	}
	fmt.Fprintln(Opts.LogBuf)
}

const header = "POTENTIAL DEADLOCK:"
