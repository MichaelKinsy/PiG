package shared

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
)

// Connection carries ordered JSON messages. Callbacks run serially on the reader; they must return promptly. Send queues a frame without waiting for the remote reader.
type Connection interface {
	Send(any) error
	OnMessage(func(json.RawMessage))
	OnClose(func())
	Close() error
}

// JSONConnection owns its reader, writer, and their shutdown. Wait joins both pumps after Close. Incomplete final lines are not messages.
type JSONConnection struct {
	input    io.ReadCloser
	output   io.WriteCloser
	cleanup  func() error
	mu       sync.Mutex
	cond     *sync.Cond
	messages []func(json.RawMessage)
	closers  []func()
	queue    [][]byte
	closed   bool
	err      error
	ready    chan struct{}
	done     chan struct{}
	start    sync.Once
	wg       sync.WaitGroup
}

// JsonConnection frames JSON over an existing duplex pair. The first OnMessage registration starts delivery, allowing the owner to install its callbacks before any frame arrives.
func JsonConnection(input io.ReadCloser, output io.WriteCloser, closeConnection func() error) *JSONConnection {
	c := &JSONConnection{input: input, output: output, cleanup: closeConnection, ready: make(chan struct{}), done: make(chan struct{})}
	c.cond = sync.NewCond(&c.mu)
	c.wg.Go(c.readLoop)
	c.wg.Go(c.writeLoop)
	return c
}

func (c *JSONConnection) OnMessage(handler func(json.RawMessage)) {
	c.mu.Lock()
	c.messages = append(c.messages, handler)
	c.mu.Unlock()
	c.start.Do(func() { close(c.ready) })
}

func (c *JSONConnection) OnClose(handler func()) {
	c.mu.Lock()
	if !c.closed {
		c.closers = append(c.closers, handler)
	}
	c.mu.Unlock()
}

func (c *JSONConnection) Send(message any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	line, err := jsonLine(message)
	if err != nil {
		return err
	}
	c.queue = append(c.queue, line)
	c.cond.Signal()
	return nil
}

func (c *JSONConnection) Close() error {
	c.shutdown(nil)
	if c.cleanup != nil {
		return errors.Join(c.Err(), c.cleanup())
	}
	return c.Err()
}

func (c *JSONConnection) Wait() { c.wg.Wait() }

// Err reports a fatal framing or IO failure. An orderly EOF or explicit close has no error.
func (c *JSONConnection) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *JSONConnection) shutdown(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.err = err
	handlers := c.closers
	c.closers = nil
	c.messages = nil
	c.queue = nil
	close(c.done)
	c.cond.Broadcast()
	c.mu.Unlock()
	// Closing the endpoints interrupts any blocked read/write before callbacks can wait for work to finish.
	_ = c.input.Close()
	_ = c.output.Close()
	for _, handler := range handlers {
		handler()
	}
}

func (c *JSONConnection) readLoop() {
	select {
	case <-c.ready:
	case <-c.done:
		return
	}
	reader := bufio.NewReader(c.input)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			c.shutdown(err)
			return
		}
		line = line[:len(line)-1]
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			var value any
			err = json.Unmarshal(line, &value)
			c.shutdown(err)
			return
		}
		c.mu.Lock()
		handlers := append([]func(json.RawMessage){}, c.messages...)
		c.mu.Unlock()
		for _, handler := range handlers {
			handler(line)
		}
	}
}

func (c *JSONConnection) writeLoop() {
	for {
		c.mu.Lock()
		for !c.closed && len(c.queue) == 0 {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		line := c.queue[0]
		c.queue[0] = nil
		c.queue = c.queue[1:]
		c.mu.Unlock()
		for len(line) > 0 {
			n, err := c.output.Write(line)
			if err == nil && n == 0 {
				err = io.ErrShortWrite
			}
			if err != nil {
				c.shutdown(err)
				return
			}
			line = line[n:]
		}
	}
}

// jsonLine uses JSON.stringify's literal HTML and Unicode separator spelling. Escaped backslashes must not be mistaken for separator escapes.
func jsonLine(message any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(message); err != nil {
		return nil, err
	}
	input := b.Bytes()
	output := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		if input[i] == '\\' && i+1 < len(input) {
			if i+6 <= len(input) && (string(input[i:i+6]) == `\u2028` || string(input[i:i+6]) == `\u2029`) {
				output = append(output, 0xe2, 0x80, 0xa8+input[i+5]-'8')
				i += 5
				continue
			}
			output = append(output, input[i], input[i+1])
			i++
			continue
		}
		output = append(output, input[i])
	}
	return output, nil
}

// ChildConnection connects the parent's view of a spawned child's pipes. The caller owns starting and waiting for the process; closing this connection also invokes kill.
func ChildConnection(stdin io.WriteCloser, stdout io.ReadCloser, kill func() error) (*JSONConnection, error) {
	if stdin == nil || stdout == nil {
		return nil, errors.New("Child process was spawned without pipes")
	}
	return JsonConnection(stdout, stdin, kill), nil
}

// ParentConnection connects a child's standard input/output. Closing stops reading from the parent.
func ParentConnection() *JSONConnection {
	return JsonConnection(os.Stdin, borrowedWriter{os.Stdout}, nil)
}

type borrowedWriter struct{ io.Writer }

func (borrowedWriter) Close() error { return nil }

// UnixTransport negotiates local connections at a filesystem socket path.
type UnixTransport struct{ path string }

func SocketTransport(path string) *UnixTransport { return &UnixTransport{path: path} }

// Listener stops accepting on Close and waits for existing connections to close, as node:net Server.close does.
type Listener struct {
	listener    net.Listener
	done        chan struct{}
	connections sync.WaitGroup
	once        sync.Once
	err         error
}

// Listen removes a stale file or socket, but never a directory, before binding the local endpoint.
func (t *UnixTransport) Listen(onConnection func(*JSONConnection)) (*Listener, error) {
	if err := syscall.Unlink(t.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	server, err := net.Listen("unix", t.path)
	if err != nil {
		return nil, err
	}
	l := &Listener{listener: server, done: make(chan struct{})}
	go l.accept(onConnection)
	return l, nil
}

func (l *Listener) accept(onConnection func(*JSONConnection)) {
	defer close(l.done)
	for {
		socket, err := l.listener.Accept()
		if err != nil {
			return
		}
		connection := JsonConnection(socket, socket, nil)
		l.connections.Add(1)
		connection.OnClose(l.connections.Done)
		onConnection(connection)
	}
}

func (l *Listener) Close() error {
	l.once.Do(func() { l.err = l.listener.Close() })
	<-l.done
	l.connections.Wait()
	return l.err
}

func (t *UnixTransport) Connect(ctx context.Context) (*JSONConnection, error) {
	var dialer net.Dialer
	socket, err := dialer.DialContext(ctx, "unix", t.path)
	if err != nil {
		return nil, err
	}
	return JsonConnection(socket, socket, nil), nil
}
