package workers

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// The IPC channel between the server and a worker process (slice 10 §3). It ports no Pi file: Node's `fork` channel with
// `serialization: "advanced"` carries `Uint8Array` link frames and structured-clone control messages on one ordered
// stream, and this is the Go form of that channel. A message is one record, in order with the others: a kind byte (0: a
// link frame, 1: a control message as JSON text), a big-endian uint32 length and the payload. A frame reaches listeners as
// `[]byte`, a control message as `json.RawMessage`: the two things `process.on("message")` tells apart by type.
//
// A write never blocks its caller (Node's `send` queues and returns false past the high-water mark): records go to an
// unbounded queue one owned writer goroutine drains. One owned reader goroutine reads and tells the listeners in order,
// and tells the disconnect once; both end with the connection. The channel implements link.IpcPort.

const (
	recordFrame   byte = 0
	recordControl byte = 1
	// maxRecord bounds one record; a longer one is a protocol error that disconnects (Node's limit is the heap's).
	maxRecord = 1 << 30
	// channelEnv tells a child where its channel is: `fd:3` (Unix) or `pipe:<name>` (Windows).
	channelEnv = "CHORD_WORKERS_CHANNEL"
)

// errChannelClosed is what a send's callback gets for a message the channel dropped.
var errChannelClosed = errors.New("workers: channel closed")

// channel is one end of an IPC channel.
type channel struct {
	connected atomic.Bool
	// acquire returns the connection, waiting for the other end if it must; cancel ends that wait and closes what it holds.
	acquire func() (net.Conn, error)
	cancel  func()

	mu         sync.Mutex
	conn       net.Conn
	message    []*messageListener
	disconnect []*disconnectListener
	queue      []outgoing
	wake       chan struct{}
	finished   bool
	started    bool
}

type outgoing struct {
	kind     byte
	payload  []byte
	callback func(error)
}

type messageListener struct{ run func(message any) }

type disconnectListener struct{ run func() }

var _ link.IpcPort = (*channel)(nil)

// newChannel makes the channel end that reads and writes conn.
func newChannel(conn net.Conn) *channel {
	return newPendingChannel(func() (net.Conn, error) { return conn, nil }, func() { _ = conn.Close() })
}

// newPendingChannel makes a channel whose connection acquire returns. It is connected from the start, as Node's
// `child.connected` is from the fork: what is sent meanwhile is queued. Nothing is read before start: the caller sets its
// listeners first, as Node's emitter loses a message nobody listens to.
func newPendingChannel(acquire func() (net.Conn, error), cancel func()) *channel {
	c := &channel{acquire: acquire, cancel: cancel, wake: make(chan struct{}, 1)}
	c.connected.Store(true)
	return c
}

// start begins reading and writing. It runs once.
func (c *channel) start() {
	c.mu.Lock()
	if c.started || c.finished {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	//portlint:allow golifetime the channel's goroutine returns when its connection ends (finish closes it)
	go c.run()
}

func (c *channel) run() {
	conn, err := c.acquire()
	if err != nil {
		c.finish()
		return
	}
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		_ = conn.Close()
		return
	}
	c.conn = conn
	c.mu.Unlock()
	//portlint:allow golifetime the writer returns when the channel finishes
	go c.write(conn)
	c.read(conn)
	c.finish()
}

func (c *channel) read(conn net.Conn) {
	var header [5]byte
	for {
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header[1:])
		if length > maxRecord || header[0] > recordControl {
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		var message any = payload
		if header[0] == recordControl {
			message = json.RawMessage(payload)
		}
		c.mu.Lock()
		listeners := slices.Clone(c.message)
		c.mu.Unlock()
		for _, listener := range listeners {
			listener.run(message)
		}
	}
}

func (c *channel) write(conn net.Conn) {
	for {
		c.mu.Lock()
		if c.finished {
			c.mu.Unlock()
			return
		}
		batch := c.queue
		c.queue = nil
		c.mu.Unlock()
		if len(batch) == 0 {
			<-c.wake
			continue
		}
		for index, record := range batch {
			var header [5]byte
			header[0] = record.kind
			binary.BigEndian.PutUint32(header[1:], uint32(len(record.payload)))
			buffers := net.Buffers{header[:], record.payload}
			_, err := buffers.WriteTo(conn)
			if record.callback != nil {
				record.callback(err)
			}
			if err != nil {
				for _, dropped := range batch[index+1:] {
					if dropped.callback != nil {
						dropped.callback(errChannelClosed)
					}
				}
				c.finish()
				return
			}
		}
	}
}

// finish ends the channel once: nothing is sent or read any more, what was queued is dropped (its callbacks told), and
// the disconnect listeners run (on the caller's goroutine: the reader's, or Disconnect's).
func (c *channel) finish() {
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		return
	}
	c.finished = true
	c.connected.Store(false)
	conn := c.conn
	dropped := c.queue
	c.queue = nil
	listeners := c.disconnect
	c.message, c.disconnect = nil, nil
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	if conn != nil {
		_ = conn.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
	for _, record := range dropped {
		if record.callback != nil {
			record.callback(errChannelClosed)
		}
	}
	for _, listener := range listeners {
		listener.run()
	}
}

// errRecordTooLarge is what a send's callback gets for a record over maxRecord, which the peer would refuse.
var errRecordTooLarge = errors.New("workers: record too large")

// enqueue queues a record for the writer; it reports whether the channel took it (not when it is over maxRecord).
func (c *channel) enqueue(kind byte, payload []byte, callback func(error)) bool {
	if len(payload) > maxRecord {
		if callback != nil {
			callback(errRecordTooLarge)
		}
		return false
	}
	c.mu.Lock()
	if c.finished {
		c.mu.Unlock()
		if callback != nil {
			callback(errChannelClosed)
		}
		return false
	}
	c.queue = append(c.queue, outgoing{kind: kind, payload: slices.Clone(payload), callback: callback})
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return true
}

// Send sends one link frame (copied, as a structured clone is). It returns false once the channel is gone (callback is
// told); the queue is Node's backpressure, which is not a failure.
func (c *channel) Send(message []byte, callback func(err error)) (bool, error) {
	return c.enqueue(recordFrame, message, callback), nil
}

// SendControl sends one control message, the JSON text of its envelope; it is dropped when the channel is gone.
func (c *channel) SendControl(payload []byte) bool {
	return c.enqueue(recordControl, payload, nil)
}

// OnMessage registers listener for frames (`[]byte`) and control messages (`json.RawMessage`), and returns its removal.
func (c *channel) OnMessage(run func(message any)) (remove func()) {
	listener := &messageListener{run: run}
	c.mu.Lock()
	if !c.finished {
		c.message = append(c.message, listener)
	}
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.message = slices.DeleteFunc(c.message, func(each *messageListener) bool { return each == listener })
	}
}

// OnDisconnect registers listener for the disconnect, which it hears once, and returns its removal.
func (c *channel) OnDisconnect(run func()) (remove func()) {
	return c.addDisconnect(run, false)
}

// prependDisconnect is `process.prependOnceListener("disconnect", ...)`: a listener that runs before the others.
func (c *channel) prependDisconnect(run func()) (remove func()) {
	return c.addDisconnect(run, true)
}

func (c *channel) addDisconnect(run func(), first bool) (remove func()) {
	listener := &disconnectListener{run: run}
	c.mu.Lock()
	if !c.finished {
		if first {
			c.disconnect = slices.Insert(c.disconnect, 0, listener)
		} else {
			c.disconnect = append(c.disconnect, listener)
		}
	}
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.disconnect = slices.DeleteFunc(c.disconnect, func(each *disconnectListener) bool { return each == listener })
	}
}

// Disconnect closes the channel (the other end hears it): the disconnect listeners run before it returns.
func (c *channel) Disconnect() { c.finish() }

// Connected reports whether the channel is open.
func (c *channel) Connected() bool { return c.connected.Load() }

// sendControlThen sends a control message and runs then once it is written, or dropped with the channel.
func (c *channel) sendControlThen(payload []byte, then func()) {
	c.enqueue(recordControl, payload, func(error) { then() })
}

var _ processPort = (*channel)(nil)
