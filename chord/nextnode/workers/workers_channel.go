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
// and tells the disconnect once; both end with the connection. A write that fails tells its send and ends the writer
// only, as a failed `send` of Node's tells its callback: the other end is gone, and the reader ends the channel once it
// told what that end wrote before. The channel implements link.IpcPort.

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
	// conn is nil while the other end has not arrived.
	acquire func() (net.Conn, error)
	cancel  func()

	mu         sync.Mutex
	conn       net.Conn
	message    []*messageListener
	disconnect []*disconnectListener
	queue      []outgoing
	wake       chan struct{}
	finished   bool
	// unwritable: a write failed; nothing is sent any more.
	unwritable bool
	started    bool
	// reads, set before the reader begins, is what it reads the connection through: a worker process's drain of it.
	reads func(conn io.Reader) io.Reader
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
	c := newPendingChannel(func() (net.Conn, error) { return conn, nil }, nil)
	c.conn = conn
	return c
}

// newPendingChannel makes a channel whose connection acquire returns. It is connected from the start, as Node's
// `child.connected` is from the fork: what is sent meanwhile is queued. Nothing is read before start: the caller sets its
// listeners first, as Node's emitter loses a message nobody listens to.
func newPendingChannel(acquire func() (net.Conn, error), cancel func()) *channel {
	c := &channel{acquire: acquire, cancel: cancel, wake: make(chan struct{}, 1)}
	c.connected.Store(true)
	return c
}

// start begins reading and writing on a goroutine of its own. It runs once.
func (c *channel) start() {
	if c.begin() {
		//portlint:allow golifetime the channel's goroutine returns when its connection ends (finish closes it)
		go c.run()
	}
}

// begin reports whether run is to be called: once, and not on a finished channel. A caller that waits for the reader calls
// run on a goroutine it owns (WorkerProcess, whose exit is told after what the reader took).
func (c *channel) begin() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.finished {
		return false
	}
	c.started = true
	return true
}

// abandon gives up the wait for an other end that has not arrived: its process ended, so nothing comes to make the
// connection, and the reader ends. A connection that was made is left to the reader: it ends with what the other end wrote.
func (c *channel) abandon() {
	c.mu.Lock()
	waiting := c.conn == nil && !c.finished
	c.mu.Unlock()
	if waiting && c.cancel != nil {
		c.cancel()
	}
}

// run is the reader: it tells what arrives, in order, on its own goroutine, and finishes the channel at the connection's end.
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
	if c.reads != nil {
		c.read(c.reads(conn))
	} else {
		c.read(conn)
	}
	c.finish()
}

func (c *channel) read(conn io.Reader) {
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
				c.mu.Lock()
				c.unwritable = true
				unsent := slices.Concat(batch[index+1:], c.queue)
				c.queue = nil
				c.mu.Unlock()
				for _, dropped := range unsent {
					if dropped.callback != nil {
						dropped.callback(errChannelClosed)
					}
				}
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
	if c.finished || c.unwritable {
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
