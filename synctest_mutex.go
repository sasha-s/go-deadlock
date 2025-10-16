//go:build goexperiment.synctest

package deadlock

import (
	"sync"
	"sync/atomic"
)

// ChannelMutex implements MutexImpl using channels for synctest compatibility
type ChannelMutex struct {
	ch     chan struct{}
	locked int32 // atomic
}

func (m *ChannelMutex) Lock() {
	m.ch <- struct{}{}
	atomic.StoreInt32(&m.locked, 1)
}

func (m *ChannelMutex) Unlock() {
	if atomic.LoadInt32(&m.locked) == 0 {
		panic("unlock of unlocked mutex")
	}
	atomic.StoreInt32(&m.locked, 0)
	<-m.ch
}

func (m *ChannelMutex) TryLock() bool {
	select {
	case m.ch <- struct{}{}:
		atomic.StoreInt32(&m.locked, 1)
		return true
	default:
		return false
	}
}

// ChannelRWMutex implements RWMutexImpl using channels for synctest compatibility
type ChannelRWMutex struct {
	readerSem   chan struct{}
	writerMutex chan struct{}
	readerCount int32
}

func (m *ChannelRWMutex) Lock() {
	m.writerMutex <- struct{}{}
}

func (m *ChannelRWMutex) Unlock() {
	<-m.writerMutex
}

func (m *ChannelRWMutex) RLock() {
	m.readerSem <- struct{}{}
	count := atomic.AddInt32(&m.readerCount, 1)
	if count == 1 {
		m.writerMutex <- struct{}{}
	}
	<-m.readerSem
}

func (m *ChannelRWMutex) RUnlock() {
	m.readerSem <- struct{}{}
	count := atomic.AddInt32(&m.readerCount, -1)
	if count < 0 {
		<-m.readerSem
		panic("RUnlock of unlocked RWMutex")
	}
	if count == 0 {
		<-m.writerMutex
	}
	<-m.readerSem
}

func (m *ChannelRWMutex) TryLock() bool {
	select {
	case m.writerMutex <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *ChannelRWMutex) TryRLock() bool {
	select {
	case m.readerSem <- struct{}{}:
		count := atomic.AddInt32(&m.readerCount, 1)
		if count == 1 {
			// Need to acquire writer mutex for first reader
			select {
			case m.writerMutex <- struct{}{}:
				<-m.readerSem
				return true
			default:
				// Failed to get writer mutex, rollback
				atomic.AddInt32(&m.readerCount, -1)
				<-m.readerSem
				return false
			}
		}
		<-m.readerSem
		return true
	default:
		return false
	}
}

func (m *ChannelRWMutex) RLocker() sync.Locker {
	return (*channelRLocker)(m)
}

type channelRLocker ChannelRWMutex

func (r *channelRLocker) Lock()   { (*ChannelRWMutex)(r).RLock() }
func (r *channelRLocker) Unlock() { (*ChannelRWMutex)(r).RUnlock() }

// Factory functions for synctest
func newChannelMutex() MutexImpl {
	return &ChannelMutex{
		ch: make(chan struct{}, 1),
	}
}

func newChannelRWMutex() RWMutexImpl {
	return &ChannelRWMutex{
		readerSem:   make(chan struct{}, 1),
		writerMutex: make(chan struct{}, 1),
	}
}

// Type aliases to override the standard mutex types for synctest
type StandardMutex = ChannelMutex
type StandardRWMutex = ChannelRWMutex
