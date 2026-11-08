package terminals

import (
	"net"
	"sync"
	"sync/atomic"
)

// frameWriter sends frames to a socket in order from its own goroutine; queued counts the bytes it has not sent yet
// (Node's `socket.writableLength`), so a peer that stops reading is noticed (MaxBacklog) instead of buffered forever.
type frameWriter struct {
	conn      net.Conn
	mu        sync.Mutex
	queue     [][]byte
	queued    int
	wake      chan struct{}
	destroyed atomic.Bool
}

func newFrameWriter(conn net.Conn) *frameWriter {
	return &frameWriter{conn: conn, wake: make(chan struct{}, 1)}
}

// run sends the queued frames until the socket is destroyed.
func (w *frameWriter) run() {
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.destroyed.Load() {
			w.mu.Unlock()
			<-w.wake
			w.mu.Lock()
		}
		if w.destroyed.Load() {
			w.mu.Unlock()
			return
		}
		frame := w.queue[0]
		w.queue[0] = nil
		w.queue = w.queue[1:]
		w.mu.Unlock()
		if _, err := w.conn.Write(frame); err != nil {
			w.destroy()
			return
		}
		w.mu.Lock()
		w.queued -= len(frame)
		w.mu.Unlock()
	}
}

// destroy closes the socket; frames not sent are dropped.
func (w *frameWriter) destroy() {
	if w.destroyed.Swap(true) {
		return
	}
	_ = w.conn.Close()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// send queues a frame. False when the socket is gone, or it is more than MaxBacklog behind (it is destroyed then).
func (w *frameWriter) send(frame []byte) bool {
	if w.destroyed.Load() {
		return false
	}
	w.mu.Lock()
	if w.queued > MaxBacklog {
		w.mu.Unlock()
		w.destroy()
		return false
	}
	w.queue = append(w.queue, frame)
	w.queued += len(frame)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return true
}
