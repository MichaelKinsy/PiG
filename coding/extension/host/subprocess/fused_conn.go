package subprocess

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"time"
)

// framePartsWriter is a connection that takes host frames by parts instead of as a byte stream: a fused extension's fusedConn (D31). Conn hands every frame it writes to it, in write order.
type framePartsWriter interface {
	WriteFrameParts(prefix, args, suffix []byte) error
}

// fusedFrame is one host frame in flight to a fused extension: its JSON is prefix + args + suffix.
type fusedFrame struct {
	prefix, args, suffix []byte
}

// fusedPipe carries host frames to a fused extension by reference. A write returns once the reader has taken the frame, as net.Pipe's does, so the host's writer queue is the only buffer and a reader that stops reading holds the writer (backpressure). The reader copies the envelope's bytes before it releases the writer and keeps only Args, which the host never writes after sending (encodedArgs).
type fusedPipe struct {
	frames    chan fusedFrame
	taken     chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	// readDeadline and writeDeadline are the extension end's read deadline and the host end's write deadline for frames, as net.Pipe applies them to bytes.
	readDeadline, writeDeadline pipeDeadline
}

func (p *fusedPipe) close() { p.closeOnce.Do(func() { close(p.closed) }) }

// fusedHostConn is the host end of a fused extension's connection. Host frames go through WriteFrameParts; the extension's frames arrive on the embedded net.Pipe end as bytes, as from a socket.
type fusedHostConn struct {
	net.Conn
	pipe *fusedPipe
}

// fusedExtConn is the extension end: ReadFrameParts gives the host's frames, and its writes go to the host through the embedded net.Pipe end.
type fusedExtConn struct {
	net.Conn
	pipe *fusedPipe
	// pending is the rest of the frame Read is returning; suffixLen is the length of the suffix of the frame ReadFrameParts returned last.
	pending   []byte
	suffixLen int
}

// newFusedConn returns the two ends of a fused extension's connection.
func newFusedConn() (*fusedHostConn, *fusedExtConn) {
	hostEnd, extEnd := net.Pipe()
	pipe := &fusedPipe{frames: make(chan fusedFrame), taken: make(chan struct{}, 1), closed: make(chan struct{})}
	pipe.readDeadline.cancel = make(chan struct{})
	pipe.writeDeadline.cancel = make(chan struct{})
	return &fusedHostConn{Conn: hostEnd, pipe: pipe}, &fusedExtConn{Conn: extEnd, pipe: pipe}
}

// errFramesByParts rejects a byte write to a fused extension: the host's frames go by parts so the extension reads them in order on one path.
var errFramesByParts = errors.New("fused connection: host frames are written by parts")

// Write rejects bytes; see errFramesByParts.
func (c *fusedHostConn) Write([]byte) (int, error) { return 0, errFramesByParts }

// WriteFrameParts hands one frame to the extension and returns once the extension has taken it. After a close of either end it returns io.ErrClosedPipe, and after the write deadline os.ErrDeadlineExceeded, as net.Pipe's Write does.
func (c *fusedHostConn) WriteFrameParts(prefix, args, suffix []byte) error {
	p := c.pipe
	select {
	case <-p.closed:
		return io.ErrClosedPipe
	case <-p.writeDeadline.wait():
		return os.ErrDeadlineExceeded
	default:
	}
	select {
	case p.frames <- fusedFrame{prefix: prefix, args: args, suffix: suffix}:
	case <-p.closed:
		return io.ErrClosedPipe
	case <-p.writeDeadline.wait():
		return os.ErrDeadlineExceeded
	}
	// The reader acknowledges every frame it received as soon as it has copied the envelope's bytes, so this wait is bounded by that copy.
	<-p.taken
	return nil
}

// SetDeadline sets both deadlines.
func (c *fusedHostConn) SetDeadline(t time.Time) error {
	c.pipe.writeDeadline.set(t)
	return c.Conn.SetDeadline(t)
}

// SetWriteDeadline sets the write deadline of frames and bytes.
func (c *fusedHostConn) SetWriteDeadline(t time.Time) error {
	c.pipe.writeDeadline.set(t)
	return c.Conn.SetWriteDeadline(t)
}

// Close closes both directions.
func (c *fusedHostConn) Close() error {
	c.pipe.close()
	return c.Conn.Close()
}

// Read reads the host's frames as the byte stream a socket carries, length prefix and all, for a peer that does not read by parts. A peer reads one way or the other: ReadFrameParts never sees the rest of a frame Read began.
func (c *fusedExtConn) Read(b []byte) (int, error) {
	if len(c.pending) == 0 {
		shell, args, err := c.ReadFrameParts()
		if err != nil {
			return 0, err
		}
		frame := make([]byte, 4, 4+len(shell)+len(args))
		if args == nil {
			frame = append(frame, shell...)
		} else {
			at := len(shell) - c.suffixLen
			frame = append(append(append(frame, shell[:at-len(argsPlaceholder)]...), args...), shell[at:]...)
		}
		binary.BigEndian.PutUint32(frame, uint32(len(frame)-4))
		c.pending = frame
	}
	n := copy(b, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// ReadFrameParts returns the next host frame: shell is a copy of the envelope's JSON with the one-byte placeholder 0 in place of a request's Args, and args is the host's Args by reference (nil when the frame has none), which nothing writes. After a close of either end it returns io.EOF, after the read deadline os.ErrDeadlineExceeded.
func (c *fusedExtConn) ReadFrameParts() (shell, args []byte, err error) {
	p := c.pipe
	select {
	case <-p.closed:
		return nil, nil, io.EOF
	case <-p.readDeadline.wait():
		return nil, nil, os.ErrDeadlineExceeded
	default:
	}
	select {
	case frame := <-p.frames:
		if frame.args == nil {
			shell = slices.Clone(frame.prefix)
		} else {
			shell = slices.Concat(frame.prefix, argsPlaceholder, frame.suffix)
		}
		c.suffixLen = len(frame.suffix)
		p.taken <- struct{}{}
		return shell, frame.args, nil
	case <-p.closed:
		return nil, nil, io.EOF
	case <-p.readDeadline.wait():
		return nil, nil, os.ErrDeadlineExceeded
	}
}

// SetDeadline sets both deadlines.
func (c *fusedExtConn) SetDeadline(t time.Time) error {
	c.pipe.readDeadline.set(t)
	return c.Conn.SetDeadline(t)
}

// SetReadDeadline sets the read deadline of host frames.
func (c *fusedExtConn) SetReadDeadline(t time.Time) error {
	c.pipe.readDeadline.set(t)
	return c.Conn.SetReadDeadline(t)
}

// Close closes both directions.
func (c *fusedExtConn) Close() error {
	c.pipe.close()
	return c.Conn.Close()
}

// pipeDeadline is net.Pipe's deadline (net/pipe.go): a channel closed when the deadline passes, replaced when it is set again.
type pipeDeadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel chan struct{}
}

// set sets the deadline; the zero time clears it, and a time in the past expires it at once.
func (d *pipeDeadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // the timer fired and closed cancel
	}
	d.timer = nil
	closed := isClosedChan(d.cancel)
	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancel = make(chan struct{})
		}
		cancel := d.cancel
		d.timer = time.AfterFunc(dur, func() { close(cancel) })
		return
	}
	if !closed {
		close(d.cancel)
	}
}

// wait returns a channel closed once the deadline has passed.
func (d *pipeDeadline) wait() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancel
}

func isClosedChan(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}
