// Package viewendpoint serves the plugin UI carrier of internal/viewcarrier on a Unix socket of an interactive session,
// so a client outside the process attaches to the view registry the terminal draws. pig additive (D614).
//
// It adds no message: a connection speaks the seven commands and two events of docs/ui-view-carrier.md and nothing else.
// The socket exists while a session's extension host is bound. When that host is unbound (a reload, a session
// replacement, the exit) every connection is closed and the socket is removed; the next host is served under the same
// path. Nothing here runs on the interactive loop: Bind and Unbind only queue, one worker goroutine creates and removes
// the socket, and each connection has its own reader and writer.
package viewendpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/viewcarrier"
)

// pig additive (D614): the bounds of the endpoint. Each is a multiple of a bound the registry or the carrier already
// has, so a client that keeps the carrier's rules never meets one.
const (
	// maxConns is the connections served at once: twice the registry's 8 attachments, so every attachment has its
	// connection and as many more can connect to be told limit. One more is closed as it is accepted.
	maxConns = 16
	// maxLineBytes is the bytes of one command line, the carrier's bound. A longer line closes the connection.
	maxLineBytes = 4 << 20
	// maxOutLines and maxOutBytes bound the lines a connection has not been written: twice the carrier's queue of 256
	// events and 4194304 bytes, which is one full event queue and as many replies. The carrier hands over one queue at
	// a time and then waits for the client, so only replies to commands the client keeps sending without reading can
	// reach the bound, and the connection is then closed.
	maxOutLines = 512
	maxOutBytes = 8 << 20
)

// Options configure an endpoint.
type Options struct {
	// Dir is the directory that holds the socket, <pid>.sock. It is created with mode 0700.
	Dir string
	// Notify is told, once per text, why no socket is served. It is called on the endpoint's worker goroutine.
	Notify func(message string)
}

// Endpoint is the view endpoint of one interactive session.
type Endpoint struct {
	opts Options
	// uid is the user every peer must be: the user of this process.
	uid int

	mu sync.Mutex
	// idle is signalled when ops, working or closed changes.
	idle *sync.Cond
	ops  []viewcarrier.Host // the hosts to serve next, in order; nil stops serving
	// working is true from a Bind or an Unbind until the worker has run every one queued.
	working bool
	started bool
	closed  bool
	path    string
	told    map[string]bool

	// worker is the one goroutine that creates and removes the socket. The first Bind or Unbind starts it and Close
	// joins it.
	worker sync.WaitGroup
	// srv is the server of the bound host. Only the worker touches it.
	srv *server
}

// New returns an endpoint that serves nothing until Bind. It starts no goroutine and touches no file.
func New(opts Options) *Endpoint {
	e := &Endpoint{opts: opts, uid: os.Geteuid(), told: map[string]bool{}}
	e.idle = sync.NewCond(&e.mu)
	return e
}

// Bind serves host's registry on the socket, after ending what was served before. It does not wait.
func (e *Endpoint) Bind(host viewcarrier.Host) { e.post(host) }

// Unbind closes every connection and removes the socket. It does not wait.
func (e *Endpoint) Unbind() { e.post(nil) }

// Close unbinds, waits until the socket is removed and every goroutine of the endpoint ended, and makes every later
// Bind and Unbind do nothing. It waits for no client: a connection is closed, not drained.
func (e *Endpoint) Close() {
	e.mu.Lock()
	e.closed = true
	e.idle.Broadcast()
	e.mu.Unlock()
	e.worker.Wait()
}

// Path returns the socket's path while one is served, and "" otherwise.
func (e *Endpoint) Path() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.path
}

func (e *Endpoint) post(host viewcarrier.Host) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || host == nil && !e.started {
		return
	}
	e.ops = append(e.ops, host)
	e.working = true
	if !e.started {
		// Close reads closed under mu before it joins the worker, so the worker never starts beside that join.
		e.started = true
		e.worker.Go(e.work)
	}
	e.idle.Broadcast()
}

// work runs the queued binds and unbinds in order until Close, which ends what is served and drops what is queued.
func (e *Endpoint) work() {
	e.mu.Lock()
	for {
		for len(e.ops) == 0 && !e.closed {
			e.working = false
			e.idle.Broadcast()
			e.idle.Wait()
		}
		if e.closed {
			e.ops = nil
			e.mu.Unlock()
			e.stop()
			e.mu.Lock()
			e.working = false
			e.idle.Broadcast()
			e.mu.Unlock()
			return
		}
		host := e.ops[0]
		e.ops = e.ops[1:]
		e.mu.Unlock()
		e.stop()
		if host != nil {
			e.start(host)
		}
		e.mu.Lock()
	}
}

func (e *Endpoint) start(host viewcarrier.Host) {
	listener, path, own, err := listen(e.opts.Dir, e.uid)
	if err != nil {
		e.tell("View endpoint: " + err.Error())
		return
	}
	s := &server{uid: e.uid, host: host, listener: listener, path: path, own: own, conns: map[*conn]struct{}{}, done: make(chan struct{})}
	e.srv = s
	e.mu.Lock()
	e.path = path
	e.mu.Unlock()
	s.work.Go(s.accept)
}

func (e *Endpoint) stop() {
	s := e.srv
	if s == nil {
		return
	}
	e.srv = nil
	e.mu.Lock()
	e.path = ""
	e.mu.Unlock()
	s.close()
}

func (e *Endpoint) tell(message string) {
	e.mu.Lock()
	seen := e.told[message]
	e.told[message] = true
	e.mu.Unlock()
	if !seen && e.opts.Notify != nil {
		e.opts.Notify(message)
	}
}

// server serves one host on one socket.
type server struct {
	uid      int
	host     viewcarrier.Host
	listener net.Listener
	path     string
	own      os.FileInfo
	done     chan struct{}
	work     sync.WaitGroup

	mu      sync.Mutex
	conns   map[*conn]struct{}
	closing bool
}

func (s *server) accept() {
	wait := 5 * time.Millisecond
	for {
		c, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// The process is out of descriptors or the like: try again later, as net/http does.
			timer := time.NewTimer(wait)
			select {
			case <-s.done:
				timer.Stop()
				return
			case <-timer.C:
			}
			wait = min(2*wait, time.Second)
			continue
		}
		wait = 5 * time.Millisecond
		s.admit(c)
	}
}

// admit serves c when its peer is this user and the endpoint has room, and closes it otherwise before reading a byte.
func (s *server) admit(c net.Conn) {
	// pig additive (D614): the peer's user is read from the kernel. The modes of the directory and the socket are not
	// the only guard, because a file system may ignore them.
	if uid, err := peerUID(c); err != nil || uid != s.uid {
		_ = c.Close()
		return
	}
	k := &conn{s: s, c: c}
	k.wake = sync.NewCond(&k.mu)
	s.mu.Lock()
	if s.closing || len(s.conns) >= maxConns {
		s.mu.Unlock()
		_ = c.Close()
		return
	}
	s.conns[k] = struct{}{}
	s.work.Go(k.write)
	s.work.Go(k.read)
	s.mu.Unlock()
}

// close ends the server: no further connection, every connection closed, the socket removed, every goroutine ended.
func (s *server) close() {
	s.mu.Lock()
	s.closing = true
	conns := make([]*conn, 0, len(s.conns))
	for k := range s.conns {
		conns = append(conns, k)
	}
	s.mu.Unlock()
	close(s.done)
	_ = s.listener.Close()
	for _, k := range conns {
		k.cut()
	}
	s.work.Wait()
	removeOwn(s.path, s.own)
}

// conn is one client connection: one carrier, a reader and a writer.
type conn struct {
	s *server
	c net.Conn

	mu   sync.Mutex
	wake *sync.Cond
	// out is the lines the writer has not taken, outBytes the bytes of the lines the client has not been written, put
	// the lines queued so far and wrote those written.
	out        [][]byte
	outBytes   int
	put, wrote uint64
	closed     bool
}

// read serves the connection's commands until it ends, then ends its attachment.
func (k *conn) read() {
	carrier := viewcarrier.New(viewcarrier.Transport{
		Host:    func() viewcarrier.Host { return k.s.host },
		Write:   k.queue,
		Flushed: k.flushed,
	})
	r := bufio.NewReaderSize(k.c, 64<<10)
	for {
		line, ok := readLine(r)
		if !ok {
			break
		}
		k.handle(carrier, line)
		// The next command is read once the client was written this one's reply, so a client that does not read is
		// owed at most one reply besides those of its intents in flight.
		if !k.written(nil) {
			break
		}
	}
	k.cut()
	carrier.Close()
	k.s.mu.Lock()
	delete(k.s.conns, k)
	k.s.mu.Unlock()
}

// readLine reads one line without its end. ok is false when the connection ended or the line is longer than
// maxLineBytes, which ends the connection.
func readLine(r *bufio.Reader) (line []byte, ok bool) {
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > maxLineBytes+1 {
			return nil, false
		}
		if err == nil {
			// The line is copied: an intent keeps its argument past the next read.
			line = append(line, part...)
			return line[:len(line)-1], true
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, false
		}
		line = append(line, part...)
	}
}

// refusal is the reply to a line that is no carrier command, in the shape of RPC mode's error response.
type refusal struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
}

func (k *conn) handle(carrier *viewcarrier.Carrier, line []byte) {
	var head struct {
		ID   json.RawMessage `json:"id"`
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		k.reply(refusal{Type: "response", Command: "parse", Error: "Failed to parse command: " + err.Error()}, nil)
		return
	}
	var command string
	// pig additive (D614): the endpoint serves the seven carrier commands and no other command of RPC mode, so a
	// client of it draws mounts and raises their intents and can do nothing else to the session.
	if json.Unmarshal(head.Type, &command) != nil || !viewcarrier.Handles(command) {
		k.reply(refusal{ID: head.ID, Type: "response", Command: command, Error: "Unknown command: " + command}, nil)
		return
	}
	carrier.Handle(head.ID, command, line, k.reply)
}

func (k *conn) reply(response any, written func()) {
	raw, err := jsstring.MarshalJSON(response)
	if err != nil {
		k.cut()
		return
	}
	k.queue(append(raw, '\n'))
	if written != nil {
		written()
	}
}

// queue appends one line to the connection's output without waiting for the client. Past the bound it cuts the
// connection instead.
func (k *conn) queue(line []byte) {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return
	}
	if k.put-k.wrote >= maxOutLines || k.outBytes >= maxOutBytes {
		k.mu.Unlock()
		k.cut()
		return
	}
	k.out = append(k.out, line)
	k.outBytes += len(line)
	k.put++
	k.wake.Broadcast()
	k.mu.Unlock()
}

// flushed returns once the client was written every line queued so far. It reports false when the connection or ctx
// ended first.
func (k *conn) flushed(ctx context.Context) bool {
	defer context.AfterFunc(ctx, func() {
		k.mu.Lock()
		k.wake.Broadcast()
		k.mu.Unlock()
	})()
	return k.written(ctx.Err)
}

// written returns once the client was written every line queued so far. It reports false when the connection ended
// first, or when ended, which is asked at each wake, reports an error.
func (k *conn) written(ended func() error) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	target := k.put
	for k.wrote < target && !k.closed && (ended == nil || ended() == nil) {
		k.wake.Wait()
	}
	return k.wrote >= target
}

// write is the connection's one writer. It alone waits on the client.
func (k *conn) write() {
	for {
		k.mu.Lock()
		for len(k.out) == 0 && !k.closed {
			k.wake.Wait()
		}
		if k.closed {
			k.mu.Unlock()
			return
		}
		batch := k.out
		k.out = nil
		k.mu.Unlock()
		for _, line := range batch {
			if _, err := k.c.Write(line); err != nil {
				k.cut()
				return
			}
			k.mu.Lock()
			k.wrote++
			k.outBytes -= len(line)
			k.wake.Broadcast()
			k.mu.Unlock()
		}
	}
}

// cut closes the connection. What it was not written is dropped.
func (k *conn) cut() {
	k.mu.Lock()
	closed := k.closed
	k.closed = true
	k.out = nil
	k.wake.Broadcast()
	k.mu.Unlock()
	if !closed {
		_ = k.c.Close()
	}
}
