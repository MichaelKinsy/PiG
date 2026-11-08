package contract

import (
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn is a WebSocket with one writer goroutine and a count of the bytes it has not yet written: Node's `send` and
// `bufferedAmount`. Sends never block; the callers watch `buffered` to pause their sources.
type wsConn struct {
	conn *websocket.Conn

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []wsItem
	bytes   int64
	closing bool
	dead    bool
	done    chan struct{}
}

type wsItem struct {
	kind  int // websocket.TextMessage, BinaryMessage, or CloseMessage
	data  []byte
	close bool
}

func newWsConn(conn *websocket.Conn) *wsConn {
	w := &wsConn{conn: conn, done: make(chan struct{})}
	w.cond = sync.NewCond(&w.mu)
	go w.write()
	return w
}

var errWsClosed = errors.New("websocket closed")

// send queues a message; false once the socket is closing or dead.
func (w *wsConn) send(kind int, data []byte) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing || w.dead {
		return false
	}
	w.queue = append(w.queue, wsItem{kind: kind, data: data})
	w.bytes += int64(len(data))
	w.cond.Signal()
	return true
}

// buffered is how many bytes are queued and not yet written.
func (w *wsConn) buffered() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bytes
}

// open is whether the socket still takes messages (`readyState === OPEN`).
func (w *wsConn) open() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.closing && !w.dead
}

// closeWith closes after what is queued, with a close frame; the peer has a moment to answer it.
func (w *wsConn) closeWith(code int, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing || w.dead {
		return
	}
	w.closing = true
	w.queue = append(w.queue, wsItem{close: true, data: websocket.FormatCloseMessage(code, reason)})
	w.cond.Signal()
}

// release ends a socket whose reader is done: what is queued behind a close frame is written first (the peer has a moment
// to answer it), then the connection and the writer go.
func (w *wsConn) release() {
	w.mu.Lock()
	closing := w.closing && !w.dead
	w.mu.Unlock()
	if closing {
		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
		}
	}
	w.terminate()
}

// terminate drops the connection at once (`WebSocket.terminate`).
func (w *wsConn) terminate() {
	w.mu.Lock()
	w.closing, w.dead = true, true
	w.queue, w.bytes = nil, 0
	w.cond.Broadcast()
	w.mu.Unlock()
	_ = w.conn.Close()
}

func (w *wsConn) write() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.dead {
			w.cond.Wait()
		}
		if w.dead {
			w.mu.Unlock()
			return
		}
		item := w.queue[0]
		w.queue[0] = wsItem{}
		w.queue = w.queue[1:]
		w.mu.Unlock()
		if item.close {
			_ = w.conn.WriteControl(websocket.CloseMessage, item.data, time.Now().Add(5*time.Second))
			// The peer answers with its own close frame, which ends the reader; if it never does, the socket goes anyway.
			time.AfterFunc(5*time.Second, func() { _ = w.conn.Close() })
			w.mu.Lock()
			w.dead = true
			w.mu.Unlock()
			return
		}
		err := w.conn.WriteMessage(item.kind, item.data)
		w.mu.Lock()
		w.bytes -= int64(len(item.data))
		w.mu.Unlock()
		if err != nil {
			w.terminate()
			return
		}
	}
}

// sendableCode is a close code one side reported, as one the other side may send (1005 and 1006 are never sent).
func sendableCode(code int) int {
	if code == 1005 {
		return 1000
	}
	if (code >= 1000 && code <= 1014 && code != 1004 && code != 1006) || (code >= 3000 && code <= 4999) {
		return code
	}
	return 1011
}

// closeInfo is the code and reason a read error ended a socket with: its close frame's, else 1006 (abnormal).
func closeInfo(err error) (int, string) {
	var closed *websocket.CloseError
	if errors.As(err, &closed) {
		return closed.Code, closed.Text
	}
	return 1006, ""
}

const (
	// pipeHighBytes pauses a channel socket's source: a direction stops reading while its destination has more than this
	// many unsent bytes, and resumes below pipeLowBytes.
	pipeHighBytes = 256 * 1024
	pipeLowBytes  = 64 * 1024
	// pollInterval is how often a paused source looks at its destination again.
	pollInterval = 10 * time.Millisecond
)

// waitDrained blocks while to has more than pipeHighBytes unsent, until it has at most pipeLowBytes or is no longer open:
// the reading goroutine does not read meanwhile, so a backlog stays in the TCP buffers behind it.
func waitDrained(to *wsConn) {
	if to.buffered() <= pipeHighBytes {
		return
	}
	for to.open() && to.buffered() > pipeLowBytes {
		time.Sleep(pollInterval)
	}
}

// pipe passes from's messages to to unchanged (text stays text) until from ends; it returns the close code and reason that
// ended it.
func pipe(from *websocket.Conn, to *wsConn) (int, string) {
	for {
		kind, data, err := from.ReadMessage()
		if err != nil {
			return closeInfo(err)
		}
		to.send(kind, data)
		waitDrained(to)
	}
}
