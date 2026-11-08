package contract

import (
	"bytes"
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// hostPort is the address of a channel's loopback target.
func (t target) hostPort() string { return net.JoinHostPort(t.host, strconv.Itoa(t.port)) }

// newLoopbackRequest is the request of a channel: method and path as the browser sent them, the headers the brain decided on
// (verbatim names), Host the target's own.
func (b *Brain) newLoopbackRequest(ctx context.Context, method, path string, headers map[string]string, to target, body io.ReadCloser, hasBody bool) (*http.Request, error) {
	address, err := url.ParseRequestURI(path)
	if err != nil {
		return nil, err
	}
	address.Scheme, address.Host = "http", to.hostPort()
	request, err := http.NewRequestWithContext(ctx, method, address.String(), nil)
	if err != nil {
		return nil, err
	}
	if hasBody {
		request.Body, request.ContentLength = body, -1
	}
	for name, value := range headers {
		request.Header[name] = []string{value}
	}
	// Node adds no `User-Agent`; Go adds its own unless the header is present and empty.
	if _, present := headers["user-agent"]; !present {
		request.Header["User-Agent"] = []string{""}
	}
	request.Host = to.hostPort()
	return request, nil
}

// headCapture is the head of one loopback response as it crossed the wire: Go's HTTP client folds header names into canonical
// case, and Pi forwards `response.rawHeaders`, the names as the server wrote them, in wire order.
type headCapture struct {
	mu    sync.Mutex
	buf   []byte
	done  bool
	pairs [][2]string
}

type headCaptureKey struct{}

// feed takes bytes read from the loopback connection until the first final response head is complete.
func (c *headCapture) feed(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return
	}
	c.buf = append(c.buf, data...)
	for {
		end := bytes.Index(c.buf, []byte("\r\n\r\n"))
		if end < 0 {
			return
		}
		head := string(c.buf[:end])
		c.buf = c.buf[end+4:]
		lines := strings.Split(head, "\r\n")
		// An interim response (100 Continue, 103 Early Hints) is not the response: its head is skipped, 101 is final.
		if fields := strings.Fields(lines[0]); len(fields) >= 2 && strings.HasPrefix(fields[1], "1") && fields[1] != "101" {
			continue
		}
		for _, line := range lines[1:] {
			colon := strings.IndexByte(line, ':')
			if colon <= 0 {
				continue
			}
			c.pairs = append(c.pairs, [2]string{line[:colon], strings.Trim(line[colon+1:], " \t")})
		}
		c.done, c.buf = true, nil
		return
	}
}

type captureConn struct {
	net.Conn
	capture *headCapture
}

func (c captureConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.capture.feed(p[:n])
	}
	return n, err
}

// newLoopbackTransport is the transport of loopback requests: one connection per request (the head belongs to its request),
// reading each response's head into the request's headCapture.
func newLoopbackTransport() *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		DisableCompression: true,
		DisableKeepAlives:  true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if capture, ok := ctx.Value(headCaptureKey{}).(*headCapture); ok {
				return captureConn{conn, capture}, nil
			}
			return conn, nil
		},
	}
}

// roundTrip sends a loopback request and returns the response with its headers as the server wrote them.
func (b *Brain) roundTrip(request *http.Request) (*http.Response, *headCapture, error) {
	capture := &headCapture{}
	response, err := b.http.RoundTrip(request.WithContext(context.WithValue(request.Context(), headCaptureKey{}, capture)))
	return response, capture, err
}

// responseHeaders is a response's headers as the relay takes them: name and value pairs in wire order, `Location` rewritten by
// the target.
func responseHeaders(capture *headCapture, to target) [][2]string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	pairs := make([][2]string, 0, len(capture.pairs))
	for _, pair := range capture.pairs {
		if to.location != nil && strings.EqualFold(pair[0], "location") {
			pair[1] = to.location(pair[1])
		}
		pairs = append(pairs, pair)
	}
	return pairs
}

type inItem struct {
	payload []byte
	end     bool
}

// httpChannel tunnels one HTTP request. `window` is how many response body bytes may be in flight before the relay returns
// credit (infinite without flow control). Body chunks beyond it wait here, and the loopback response is not read, so the
// server only produces what the browser actually reads.
type httpChannel struct {
	b      *Brain
	s      *session
	id     string
	head   requestHead
	header map[string]string
	to     target
	flow   bool
	ctx    context.Context
	cancel context.CancelFunc

	inMu     sync.Mutex
	inCond   *sync.Cond
	in       []inItem
	inClosed bool
	body     *io.PipeWriter

	mu        sync.Mutex
	cond      *sync.Cond
	available int64
	pending   [][]byte
	ended     bool
	waiting   bool
	congested bool
	closed    bool
	source    bool
}

func (b *Brain) openHTTP(s *session, id string, head requestHead, headers map[string]string, window int64, to target) {
	ctx, cancel := context.WithCancel(b.ctx)
	h := &httpChannel{b: b, s: s, id: id, head: head, header: headers, to: to, flow: window != math.MaxInt64, ctx: ctx, cancel: cancel, available: window}
	h.inCond = sync.NewCond(&h.inMu)
	h.cond = sync.NewCond(&h.mu)
	s.put(id, &channel{
		data: func(payload []byte, kind byte) {
			h.inMu.Lock()
			h.in = append(h.in, inItem{payload: payload, end: kind == frameEnd})
			h.inMu.Unlock()
			h.inCond.Signal()
		},
		close: h.destroy,
		credit: func(bytes int64) {
			h.mu.Lock()
			h.available += bytes
			h.flushLocked()
			h.mu.Unlock()
			h.cond.Broadcast()
		},
		congested: func(paused bool) {
			h.mu.Lock()
			h.congested = paused
			h.mu.Unlock()
			h.cond.Broadcast()
		},
	})
	b.spawn(h.pump)
}

func (h *httpChannel) destroy() {
	h.cancel()
	h.inMu.Lock()
	h.inClosed = true
	pw := h.body
	h.inMu.Unlock()
	h.inCond.Broadcast()
	if pw != nil {
		_ = pw.CloseWithError(context.Canceled)
	}
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.cond.Broadcast()
}

func (h *httpChannel) next() (inItem, bool) {
	h.inMu.Lock()
	defer h.inMu.Unlock()
	for len(h.in) == 0 && !h.inClosed {
		h.inCond.Wait()
	}
	if h.inClosed {
		return inItem{}, false
	}
	item := h.in[0]
	h.in[0] = inItem{}
	h.in = h.in[1:]
	return item, true
}

// pump starts the loopback request with the first of the relay's frames, and feeds it the request body: the relay is credited
// once the loopback request has taken the bytes, not when they arrive here.
func (h *httpChannel) pump() {
	first, ok := h.next()
	if !ok {
		return
	}
	if first.end && len(first.payload) == 0 {
		h.b.spawn(func() { h.roundTrip(nil) })
		return
	}
	reader, writer := io.Pipe()
	h.inMu.Lock()
	h.body = writer
	closed := h.inClosed
	h.inMu.Unlock()
	if closed {
		_ = writer.Close()
		return
	}
	h.b.spawn(func() { h.roundTrip(reader) })
	for item, ok := first, true; ok; item, ok = h.next() {
		if len(item.payload) > 0 {
			if _, err := writer.Write(item.payload); err != nil {
				return
			}
			if h.flow && h.s.has(h.id) {
				h.s.sendControl(map[string]any{"version": protocolVersion, "type": "credit", "connection_id": h.id, "bytes": len(item.payload)})
			}
		}
		if item.end {
			_ = writer.Close()
			return
		}
	}
	_ = writer.CloseWithError(context.Canceled)
}

func (h *httpChannel) roundTrip(body io.ReadCloser) {
	request, err := h.b.newLoopbackRequest(h.ctx, h.head.Method, h.head.Path, h.header, h.to, body, body != nil)
	if err != nil {
		h.fail()
		return
	}
	response, capture, err := h.b.roundTrip(request)
	if err != nil {
		h.fail()
		return
	}
	defer response.Body.Close()
	h.mu.Lock()
	h.source = true
	h.mu.Unlock()
	h.s.sendControl(map[string]any{
		"version": protocolVersion, "type": "response", "connection_id": h.id,
		"status": response.StatusCode, "headers": responseHeaders(capture, h.to),
	})
	buffer := make([]byte, 64*1024)
	for {
		h.mu.Lock()
		for (h.waiting || h.congested) && !h.closed {
			h.cond.Wait()
		}
		closed := h.closed
		h.mu.Unlock()
		if closed {
			return
		}
		n, err := response.Body.Read(buffer)
		h.mu.Lock()
		if n > 0 {
			h.pending = append(h.pending, append([]byte(nil), buffer[:n]...))
		}
		if err != nil {
			h.ended = true
		}
		h.flushLocked()
		h.mu.Unlock()
		if err != nil {
			if err != io.EOF {
				h.fail()
			}
			return
		}
	}
}

// flushLocked sends what the window allows, and ends the channel once everything went.
func (h *httpChannel) flushLocked() {
	for len(h.pending) > 0 && h.available > 0 {
		chunk := h.pending[0]
		if int64(len(chunk)) > h.available {
			h.pending[0] = chunk[h.available:]
			chunk = chunk[:h.available]
		} else {
			h.pending = h.pending[1:]
		}
		h.available -= int64(len(chunk))
		for offset := 0; offset < len(chunk); offset += bodyChunkBytes {
			h.s.sendData(h.id, chunk[offset:min(offset+bodyChunkBytes, len(chunk))], frameBinary)
		}
	}
	if len(h.pending) == 0 && h.ended && h.s.has(h.id) {
		h.s.sendData(h.id, nil, frameEnd)
		h.s.remove(h.id)
	}
	h.waiting = len(h.pending) > 0
	h.cond.Broadcast()
}

// fail is the loopback request's error: an unavailable forward answers 502 while nothing was answered yet, anything else
// closes the channel.
func (h *httpChannel) fail() {
	if !h.s.remove(h.id) {
		return
	}
	h.mu.Lock()
	source := h.source
	h.mu.Unlock()
	if !source && h.to.unavailable != "" {
		h.s.respond(h.id, 502, h.to.unavailable)
	} else {
		h.s.closeChannel(h.id, 0)
	}
	h.destroy()
}

func (b *Brain) openSocket(s *session, id string, head requestHead, headers map[string]string, to target) {
	if s.channelSockets {
		b.openChannelSocket(s, id, head, headers, to)
	} else {
		b.openWebSocket(s, id, head, headers, to)
	}
}

// dialLoopback is the loopback WebSocket of a channel, with the browser's subprotocols.
func (b *Brain) dialLoopback(ctx context.Context, path string, headers map[string]string, to target) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	var protocols []string
	for name, value := range headers {
		switch name {
		case "sec-websocket-protocol":
			for _, protocol := range strings.Split(value, ",") {
				protocols = append(protocols, strings.TrimSpace(protocol))
			}
		case "sec-websocket-key", "sec-websocket-version", "sec-websocket-extensions":
		default:
			header[name] = []string{value}
		}
	}
	dialer := &websocket.Dialer{HandshakeTimeout: b.handshakeTimeout, Subprotocols: protocols}
	conn, response, err := dialer.DialContext(ctx, "ws://"+to.hostPort()+path, header)
	if err == nil {
		conn.SetReadLimit(maxMessageBytes)
	}
	return conn, response, err
}

// relayChannel dials a channel's own socket to Radius (relay protocol v5), authenticated like the host socket.
func (b *Brain) relayChannel(config *Config, id string) (*websocket.Conn, error) {
	address := websocketURL(config.Gateway, "/v1/servers/"+config.ServerID+"/channels/"+id)
	conn, response, err := b.dialer([]string{channelSubprotocol}).DialContext(b.ctx, address, http.Header{"Authorization": {"Bearer " + config.Credential.Access}})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	conn.SetReadLimit(maxMessageBytes)
	return conn, nil
}

// openChannelSocket is a WebSocket channel of a v5 relay: its own socket to Radius, piped to the loopback socket. Whichever
// side closes first closes the other with its code. Until the channel socket is connected the relay is still waiting for it,
// so a channel that ends before (its socket or the loopback failed) is refused on the host socket: the relay then fails the
// client's upgrade at once instead of at its timeout.
func (b *Brain) openChannelSocket(s *session, id string, head requestHead, headers map[string]string, to target) {
	var mu sync.Mutex
	ended, paired := false, false
	var sockets []*wsConn
	end := func(code int, reason string, tell func(paired bool) bool) {
		mu.Lock()
		if ended {
			mu.Unlock()
			return
		}
		ended = true
		tellRelay := tell(paired)
		open := slices.Clone(sockets)
		mu.Unlock()
		s.remove(id)
		if tellRelay {
			s.closeChannel(id, 1011)
		}
		for _, socket := range open {
			socket.closeWith(sendableCode(code), reason)
		}
	}
	byDefault := func(paired bool) bool { return !paired }
	never := func(bool) bool { return false }
	ctx, cancel := context.WithCancel(b.ctx)
	s.put(id, &channel{
		data: func([]byte, byte) {},
		// The relay ended the channel itself.
		close:     func() { end(1001, "", never); cancel() },
		credit:    func(int64) {},
		congested: func(bool) {},
	})
	b.spawn(func() {
		defer cancel()
		config, err := b.currentConfig()
		if err != nil {
			end(1011, "", byDefault)
			return
		}
		var channelConn, upstreamConn *websocket.Conn
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			conn, err := b.relayChannel(config, id)
			if err != nil {
				end(1011, "", byDefault)
				return
			}
			mu.Lock()
			if ended {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			paired = true
			channelConn = conn
			sockets = append(sockets, newWsConn(conn))
			mu.Unlock()
		}()
		go func() {
			defer group.Done()
			conn, response, err := b.dialLoopback(ctx, head.Path, headers, to)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if err != nil {
				code, _ := closeInfo(err)
				end(code, "", byDefault)
				return
			}
			mu.Lock()
			if ended {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			upstreamConn = conn
			sockets = append(sockets, newWsConn(conn))
			mu.Unlock()
		}()
		group.Wait()
		mu.Lock()
		if ended || channelConn == nil || upstreamConn == nil {
			mu.Unlock()
			return
		}
		toChannel, toUpstream := sockets[0], sockets[1]
		if toChannel.conn != channelConn {
			toChannel, toUpstream = toUpstream, toChannel
		}
		mu.Unlock()
		var pipes sync.WaitGroup
		pipes.Add(2)
		go func() {
			defer pipes.Done()
			code, reason := pipe(channelConn, toUpstream)
			end(code, reason, byDefault)
		}()
		go func() {
			defer pipes.Done()
			code, reason := pipe(upstreamConn, toChannel)
			end(code, reason, byDefault)
		}()
		pipes.Wait()
		mu.Lock()
		all := slices.Clone(sockets)
		mu.Unlock()
		for _, socket := range all {
			socket.release()
		}
		_ = channelConn.Close()
		_ = upstreamConn.Close()
	})
}

// openWebSocket is a WebSocket channel on the host socket (relays before v5).
func (b *Brain) openWebSocket(s *session, id string, head requestHead, headers map[string]string, to target) {
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	var upstream *wsConn
	var queued []inItem
	paused, closed := false, false
	var conn *websocket.Conn
	finish := func() {
		if s.remove(id) {
			s.closeChannel(id, 1000)
		}
	}
	ctx, cancel := context.WithCancel(b.ctx)
	s.put(id, &channel{
		data: func(payload []byte, kind byte) {
			if kind == frameEnd {
				return
			}
			messageKind := websocket.BinaryMessage
			if kind == frameText {
				messageKind = websocket.TextMessage
			}
			mu.Lock()
			defer mu.Unlock()
			if upstream != nil {
				upstream.send(messageKind, payload)
			} else {
				queued = append(queued, inItem{payload: append([]byte(nil), payload...), end: messageKind == websocket.TextMessage})
			}
		},
		close: func() {
			cancel()
			mu.Lock()
			closed = true
			if upstream != nil {
				upstream.closeWith(1000, "")
			}
			mu.Unlock()
			cond.Broadcast()
		},
		credit: func(int64) {},
		congested: func(pause bool) {
			// Stops reading the loopback socket; the server's own backlog checks then see a slow reader.
			mu.Lock()
			paused = pause
			mu.Unlock()
			cond.Broadcast()
		},
	})
	b.spawn(func() {
		defer cancel()
		dialed, response, err := b.dialLoopback(ctx, head.Path, headers, to)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			finish()
			return
		}
		conn = dialed
		defer conn.Close()
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		upstream = newWsConn(conn)
		defer upstream.release()
		for _, item := range queued {
			kind := websocket.BinaryMessage
			if item.end {
				kind = websocket.TextMessage
			}
			upstream.send(kind, item.payload)
		}
		queued = nil
		mu.Unlock()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			frame := frameBinary
			if kind == websocket.TextMessage {
				frame = frameText
			}
			s.sendData(id, data, frame)
			mu.Lock()
			for paused && !closed {
				cond.Wait()
			}
			mu.Unlock()
		}
		finish()
	})
}
