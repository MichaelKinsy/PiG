// SPDX-License-Identifier: MIT

package sqlhost

import "sync"

// mailbox is an unbounded FIFO of owner work. Producers never block, so an effect goroutine posting a completion cannot
// deadlock against an owner that is itself waiting for the effect.
type mailbox struct {
	mu     sync.Mutex
	items  []func(error)
	head   int // items before head ran; their slots are nil
	wake   chan struct{}
	closed bool
}

func newMailbox() *mailbox { return &mailbox{wake: make(chan struct{}, 1)} }

// put queues work and reports whether the mailbox still accepts it.
func (m *mailbox) put(work func(error)) bool {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	if m.head > 0 && len(m.items) == cap(m.items) {
		// A full slice moves its live items to the front instead of growing: the queue keeps one backing array while it drains and refills.
		m.items = m.items[:copy(m.items, m.items[m.head:])]
		m.head = 0
	}
	m.items = append(m.items, work)
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return true
}

// take blocks for the next item. It returns false once the mailbox is closed and drained.
func (m *mailbox) take() (func(error), bool) {
	for {
		m.mu.Lock()
		if m.head < len(m.items) {
			work := m.items[m.head]
			m.items[m.head] = nil
			m.head++
			if m.head == len(m.items) {
				m.items, m.head = m.items[:0], 0
			}
			m.mu.Unlock()
			return work, true
		}
		closed := m.closed
		m.mu.Unlock()
		if closed {
			return nil, false
		}
		<-m.wake
	}
}

// close stops accepting work. Queued items still run.
func (m *mailbox) close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// drain removes and returns the items that never ran.
func (m *mailbox) drain() []func(error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.items[m.head:]
	m.items, m.head = nil, 0
	return items
}
