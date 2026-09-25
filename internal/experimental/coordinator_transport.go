package experimental

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
)

// controlMessage retains absent payloads separately from JSON null and never interprets routed data.
type controlMessage struct {
	Type               string          `json:"type"`
	Protocol           json.RawMessage `json:"protocol,omitempty"`
	ServerConnectionID string          `json:"serverConnectionId,omitempty"`
	Endpoint           string          `json:"endpoint,omitempty"`
	PeerID             string          `json:"peerId,omitempty"`
	To                 string          `json:"to,omitempty"`
	From               string          `json:"from,omitempty"`
	Payload            json.RawMessage `json:"payload,omitempty"`
}

func readControlLines(reader io.Reader, handle func(json.RawMessage) error) error {
	r := bufio.NewReader(reader)
	var buffered []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buffered)+len(chunk) > MaxControlLineBytes {
			return errors.New("Coordinator message is too large")
		}
		buffered = append(buffered, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return err
		}
		line := bytes.TrimSuffix(buffered, []byte{'\n'})
		if !json.Valid(line) {
			return errors.New("Coordinator sent invalid JSON")
		}
		if err := deliverControlLine(handle, line); err != nil {
			return err
		}
		buffered = nil
	}
}

// Node catches exceptions thrown by a message listener at the JSON-line callback boundary.
func deliverControlLine(handle func(json.RawMessage) error, line json.RawMessage) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = errors.New("Coordinator sent invalid JSON")
		}
	}()
	return handle(line)
}

func decodeControlMessage(line json.RawMessage) (controlMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(line, &fields); err != nil {
		return controlMessage{}, err
	}
	if value, ok := fields["type"]; !ok || len(value) == 0 || value[0] != '"' {
		return controlMessage{}, errors.New("Coordinator message must have a type")
	}
	stringValue := func(name string) string {
		var value string
		if err := json.Unmarshal(fields[name], &value); err != nil {
			return ""
		}
		return value
	}
	return controlMessage{
		Type: stringValue("type"), Protocol: fields["protocol"],
		ServerConnectionID: stringValue("serverConnectionId"), Endpoint: stringValue("endpoint"),
		PeerID: stringValue("peerId"), To: stringValue("to"), From: stringValue("from"), Payload: fields["payload"],
	}, nil
}

func writeControlLine(conn net.Conn, message any) error {
	line, err := EncodeControlLine(message)
	if err != nil {
		return err
	}
	_, err = io.WriteString(conn, line)
	return err
}

// routedSocket preserves Node's ordered, nonblocking socket.write contract for the router.
// Pending data is retained until written or the socket closes; a slow peer never holds the router lock.
type routedSocket struct {
	net.Conn
	mu     sync.Mutex
	ready  *sync.Cond
	queue  []string
	closed bool
}

func newRoutedSocket(conn net.Conn) *routedSocket {
	socket := &routedSocket{Conn: conn}
	socket.ready = sync.NewCond(&socket.mu)
	return socket
}

func (s *routedSocket) enqueue(message any) error {
	line, err := EncodeControlLine(message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.queue = append(s.queue, line)
		s.ready.Signal()
	}
	return nil
}

func (s *routedSocket) writeLoop() {
	defer s.close()
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.closed {
			s.ready.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		line := s.queue[0]
		s.queue[0] = ""
		s.queue = s.queue[1:]
		s.mu.Unlock()
		if _, err := io.WriteString(s.Conn, line); err != nil {
			return
		}
	}
}

func (s *routedSocket) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.queue = nil
	s.ready.Broadcast()
	_ = s.Close()
}
