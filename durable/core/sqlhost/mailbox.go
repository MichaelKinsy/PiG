// SPDX-License-Identifier: MIT

package sqlhost

import "sync"

// mailbox is an unbounded FIFO of owner work. Producers never block, so an effect goroutine posting a completion cannot
// deadlock against an owner that is itself waiting for the effect.
type mailbox struct {
	mu     sync.Mutex
	items  []func(error)
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
		if len(m.items) > 0 {
			work := m.items[0]
			m.items[0] = nil
			m.items = m.items[1:]
			if len(m.items) == 0 {
				m.items = nil
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
	items := m.items
	m.items = nil
	return items
}
