package agent

import (
	"slices"
	"sync"
)

// QueueMode controls how PendingMessageQueue.Drain behaves.
// Mirrors upstream packages/agent/src/agent.ts:55 (type QueueMode).
type QueueMode string

const (
	// QueueModeAll drains all queued messages at once.
	QueueModeAll QueueMode = "all"
	// QueueModeOneAtATime drains one message per call.
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// PendingMessageQueue is a thread-safe FIFO of agent messages with a
// configurable drain policy. Used for steering and follow-up queues.
//
// Mirrors upstream packages/agent/src/agent.ts:113 (class PendingMessageQueue).
type PendingMessageQueue struct {
	mu       sync.Mutex
	messages []AgentMessage
	mode     QueueMode
}

// NewPendingMessageQueue creates a queue with the given drain mode.
func NewPendingMessageQueue(mode QueueMode) *PendingMessageQueue {
	return &PendingMessageQueue{mode: mode}
}

// Enqueue appends a message to the queue.
func (q *PendingMessageQueue) Enqueue(msg AgentMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, msg)
}

// HasItems reports whether the queue has any pending messages.
func (q *PendingMessageQueue) HasItems() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages) > 0
}

// Peek returns the messages the next Drain would remove, without removing
// them: every message in "all" mode, the oldest one in "one-at-a-time" mode.
// Mirrors upstream PendingMessageQueue.peek.
func (q *PendingMessageQueue) Peek() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.peekLocked()
}

func (q *PendingMessageQueue) peekLocked() []AgentMessage {
	if len(q.messages) == 0 {
		return nil
	}
	if q.mode == QueueModeAll {
		return slices.Clone(q.messages)
	}
	return []AgentMessage{q.messages[0]}
}

// Drain removes and returns pending messages according to the queue's mode.
// "all" drains every message; "one-at-a-time" drains the oldest single message.
// Returns nil when the queue is empty. Mirrors upstream PendingMessageQueue.drain.
func (q *PendingMessageQueue) Drain() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	drained := q.peekLocked()
	q.messages = q.messages[len(drained):]
	return drained
}

// Clear removes and returns all queued messages regardless of mode.
func (q *PendingMessageQueue) Clear() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	cleared := q.messages
	q.messages = nil
	return cleared
}

// SetMode changes the drain mode.
func (q *PendingMessageQueue) SetMode(mode QueueMode) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.mode = mode
}

// Mode returns the current drain mode.
func (q *PendingMessageQueue) Mode() QueueMode {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.mode
}

// Len returns the number of queued messages.
func (q *PendingMessageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

// Messages returns copies of all queued messages without draining, whatever
// the mode. Used for UI display of pending steering/follow-up messages.
func (q *PendingMessageQueue) Messages() []AgentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return nil
	}
	return slices.Clone(q.messages)
}
