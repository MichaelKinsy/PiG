package codingagent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// livePeerFrame is one frame the host wrote to a livePeer: its bytes and the envelope they decode to.
type livePeerFrame struct {
	raw      []byte
	envelope subprocess.Envelope
}

// kind names the frame by its envelope type and, for a notification, its method.
func (f livePeerFrame) kind() string {
	if f.envelope.Notify != nil {
		return f.envelope.Type + ":" + f.envelope.Notify.Method
	}
	return f.envelope.Type
}

// livePeer is the extension end of a net.Pipe connection, reading from the moment the host starts the connection.
//
// A net.Pipe has no buffer, so a frame the host queues stays outstanding work until the peer reads it. A peer that reads only after the host
// finished its own work leaves the first frame unwritten for as long as that work takes. The host's liveness check (pig divergence (D56))
// pings after 2s of outstanding work and fails the connection after 5s more without write progress, which a loaded machine reaches while it builds
// a catalog of more than 1 MB. A live runtime reads concurrently and answers each ping, so livePeer does too, and the frames it records are the
// host's own: a heartbeat ping is transport, not something the host published.
type livePeer struct {
	conn net.Conn

	mu     sync.Mutex
	cond   *sync.Cond
	frames []livePeerFrame
	err    error
	done   chan struct{}
}

func newLivePeer(conn net.Conn) *livePeer {
	p := &livePeer{conn: conn, done: make(chan struct{})}
	p.cond = sync.NewCond(&p.mu)
	go p.run()
	return p
}

func (p *livePeer) run() {
	defer close(p.done)
	for {
		frame, err := p.readFrame()
		if err == nil && frame.envelope.Type == subprocess.MsgPing {
			err = p.pong(frame.envelope.Ping)
		}
		p.mu.Lock()
		switch {
		case err != nil:
			p.err = err
		case frame.envelope.Type != subprocess.MsgPing:
			p.frames = append(p.frames, frame)
		}
		p.mu.Unlock()
		p.cond.Broadcast()
		if err != nil {
			return
		}
	}
}

func (p *livePeer) readFrame() (livePeerFrame, error) {
	var header [4]byte
	if _, err := io.ReadFull(p.conn, header[:]); err != nil {
		return livePeerFrame{}, err
	}
	frame := livePeerFrame{raw: make([]byte, binary.BigEndian.Uint32(header[:]))}
	if _, err := io.ReadFull(p.conn, frame.raw); err != nil {
		return livePeerFrame{}, err
	}
	return frame, json.Unmarshal(frame.raw, &frame.envelope)
}

func (p *livePeer) pong(ping *subprocess.PingPayload) error {
	if ping == nil {
		return errors.New("ping frame without a ping payload")
	}
	body, err := json.Marshal(&subprocess.Envelope{Type: subprocess.MsgPong, Pong: &subprocess.PongPayload{Nonce: ping.Nonce}})
	if err != nil {
		return err
	}
	_, err = p.conn.Write(append(binary.BigEndian.AppendUint32(nil, uint32(len(body))), body...))
	return err
}

// untilNotify returns the frames received before the notification with the given method, in order, and consumes them and that notification.
// It waits for the notification, and returns the connection's read error if the connection ends first.
func (p *livePeer) untilNotify(method string) ([]livePeerFrame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if i := slices.IndexFunc(p.frames, func(f livePeerFrame) bool { return f.envelope.Notify != nil && f.envelope.Notify.Method == method }); i >= 0 {
			before := slices.Clone(p.frames[:i])
			p.frames = p.frames[i+1:]
			return before, nil
		}
		if p.err != nil {
			return nil, p.err
		}
		p.cond.Wait()
	}
}

// close ends the connection and waits for the reading goroutine.
func (p *livePeer) close() {
	_ = p.conn.Close()
	<-p.done
}
