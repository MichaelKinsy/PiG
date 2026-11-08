package contract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// clientChannelRequest is a client channel's request (see "Client channels").
type clientChannelRequest struct {
	Type    string
	Method  string
	Path    string
	Headers map[string]string
}

var requestMethod = regexp.MustCompile(`^[A-Z]{1,16}$`)

// clientRequest is a client channel's first message, from the wire: nil unless it is a request for a path on this server.
func clientRequest(data []byte) *clientChannelRequest {
	var value struct {
		Type    string          `json:"type"`
		Method  any             `json:"method"`
		Path    any             `json:"path"`
		Headers json.RawMessage `json:"headers"`
	}
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	method, isMethod := value.Method.(string)
	path, isPath := value.Path.(string)
	if (value.Type != "http" && value.Type != "websocket") || (value.Type == "http" && (!isMethod || !requestMethod.MatchString(method))) ||
		!isPath || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil
	}
	headers, ok := stringMap(value.Headers)
	if !ok {
		return nil
	}
	if !isMethod {
		method = "GET"
	}
	return &clientChannelRequest{Type: value.Type, Method: method, Path: path, Headers: headers}
}

// stringMap is a JSON object whose values are all strings (not an array, not null).
func stringMap(raw json.RawMessage) (map[string]string, bool) {
	var object map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	out := make(map[string]string, len(object))
	for name, value := range object {
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		out[name] = text
	}
	return out, true
}

// openClientChannel is a client channel of the owner (see "Client channels"): its own socket to Radius, whose first message
// is the request. Ends like openChannelSocket: refused on the host socket until the channel socket is connected.
func (b *Brain) openClientChannel(s *session, id string) {
	if !s.channelSockets {
		s.closeChannel(id, 1008)
		return
	}
	var mu sync.Mutex
	ended, paired := false, false
	var out *wsConn
	stop := func() {}
	end := func(code int, tellRelay bool) {
		mu.Lock()
		if ended {
			mu.Unlock()
			return
		}
		ended = true
		tellRelay = tellRelay && !paired
		current, abandon := out, stop
		mu.Unlock()
		s.remove(id)
		if tellRelay {
			s.closeChannel(id, 1011)
		}
		abandon()
		if current != nil {
			current.closeWith(sendableCode(code), "")
		}
	}
	endByDefault := func(code int) {
		mu.Lock()
		tell := !paired
		mu.Unlock()
		end(code, tell)
	}
	s.put(id, &channel{data: func([]byte, byte) {}, close: func() { end(1001, false) }, credit: func(int64) {}, congested: func(bool) {}})
	b.spawn(func() {
		config, err := b.currentConfig()
		if err != nil {
			endByDefault(1011)
			return
		}
		conn, err := b.relayChannel(config, id)
		if err != nil {
			endByDefault(1011)
			return
		}
		defer conn.Close()
		mu.Lock()
		if ended {
			mu.Unlock()
			return
		}
		paired = true
		out = newWsConn(conn)
		mu.Unlock()
		defer out.release()
		kind, first, err := conn.ReadMessage()
		if err != nil {
			code, _ := closeInfo(err)
			end(code, false)
			return
		}
		request := (*clientChannelRequest)(nil)
		if kind == websocket.TextMessage {
			request = clientRequest(first)
		}
		if request == nil {
			end(1008, false)
			return
		}
		origin := clientOrigin
		b.mu.Lock()
		if b.config != nil && b.config.PublicOrigin != "" {
			origin = b.config.PublicOrigin
		}
		b.mu.Unlock()
		if b.forwards != nil && b.forwards.RadiusOrigin() != "" {
			origin = b.forwards.RadiusOrigin()
		}
		headers := map[string]string{}
		for rawName, value := range request.Headers {
			name := strings.ToLower(rawName)
			if !droppedHeaders[name] && name != "cookie" && name != "origin" {
				headers[name] = value
			}
		}
		// The owner by Radius alone, as on the brain's UI, at the origin it reports back to the client.
		headers[RelaySecretHeader] = b.relay.Secret()
		headers[RelayOriginHeader] = origin
		headers["origin"] = origin
		to := target{host: "127.0.0.1", port: b.port()}
		answer := func(status int, pairs [][2]string) {
			if pairs == nil {
				pairs = [][2]string{}
			}
			encoded, _ := json.Marshal(struct {
				Status  int         `json:"status"`
				Headers [][2]string `json:"headers"`
				Origin  string      `json:"origin"`
			}{status, pairs, origin})
			out.send(websocket.TextMessage, encoded)
		}
		if request.Type == "websocket" {
			b.clientSocket(conn, out, request.Path, headers, to, answer, end, func(abandon func()) {
				mu.Lock()
				stop = abandon
				mu.Unlock()
			})
		} else {
			b.clientHTTP(conn, out, request, headers, to, answer, end, func(abandon func()) {
				mu.Lock()
				stop = abandon
				mu.Unlock()
			})
		}
	})
}

// clientHTTP is a client channel's HTTP request. Its goroutines pace the channel: the request body is read from the client
// only as fast as the loopback request takes it, and the response is written to it only as fast as it drains.
func (b *Brain) clientHTTP(conn *websocket.Conn, out *wsConn, request *clientChannelRequest, headers map[string]string, to target, answer func(int, [][2]string), end func(int, bool), setStop func(func())) {
	ctx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	setStop(cancel)
	// The request body, then the client's text end marker; the request starts with the first of them.
	kind, data, err := conn.ReadMessage()
	if err != nil {
		code, _ := closeInfo(err)
		end(code, false)
		return
	}
	var writer *io.PipeWriter
	var body io.ReadCloser
	if kind == websocket.BinaryMessage {
		var reader *io.PipeReader
		reader, writer = io.Pipe()
		body = reader
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		upstream, err := b.newLoopbackRequest(ctx, request.Method, request.Path, headers, to, body, body != nil)
		if err != nil {
			end(1011, false)
			return
		}
		response, capture, err := b.roundTrip(upstream)
		if err != nil {
			end(1011, false)
			return
		}
		defer response.Body.Close()
		answer(response.StatusCode, responseHeaders(capture, to))
		buffer := make([]byte, bodyChunkBytes)
		for {
			n, err := response.Body.Read(buffer)
			if n > 0 {
				out.send(websocket.BinaryMessage, append([]byte(nil), buffer[:n]...))
				waitDrained(out)
			}
			if err == io.EOF {
				end(1000, false)
				return
			}
			if err != nil {
				end(1011, false)
				return
			}
		}
	}()
	if writer != nil {
		for {
			if kind == websocket.BinaryMessage {
				if _, err := writer.Write(data); err != nil {
					break
				}
			} else {
				_ = writer.Close()
				break
			}
			kind, data, err = conn.ReadMessage()
			if err != nil {
				code, _ := closeInfo(err)
				_ = writer.CloseWithError(context.Canceled)
				end(code, false)
				break
			}
		}
		// The response may outlast the request body; it ends the channel.
	}
	// Whatever else the client sends is ignored; this only notices that it closed, and waits for its answer to the close.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				code, _ := closeInfo(err)
				end(code, false)
				return
			}
		}
	}()
	<-done
	<-readerDone
}

// clientSocket is a client channel's WebSocket: answered 101 once the loopback socket is open, then piped both ways.
func (b *Brain) clientSocket(conn *websocket.Conn, out *wsConn, path string, headers map[string]string, to target, answer func(int, [][2]string), end func(int, bool), setStop func(func())) {
	ctx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	var current *websocket.Conn
	var mu sync.Mutex
	setStop(func() {
		cancel()
		mu.Lock()
		defer mu.Unlock()
		if current != nil {
			_ = current.Close()
		}
	})
	upstream, response, err := b.dialLoopback(ctx, path, headers, to)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
			// With a refused upgrade the answer is the server's status.
			answer(response.StatusCode, nil)
			end(1000, false)
			return
		}
		end(1011, false)
		return
	}
	mu.Lock()
	current = upstream
	mu.Unlock()
	defer upstream.Close()
	answer(http.StatusSwitchingProtocols, nil)
	toUpstream := newWsConn(upstream)
	defer toUpstream.release()
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		code, _ := pipe(conn, toUpstream)
		end(code, false)
	}()
	go func() {
		defer group.Done()
		code, _ := pipe(upstream, out)
		end(code, false)
	}()
	group.Wait()
}
