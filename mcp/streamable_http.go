package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Ports packages/mcp/src/transports/streamable-http.ts.

const (
	maxErrorBodyBytes           = 8 * 1024
	errorMessageBodyChars       = 500
	defaultReconnectInitialMs   = 1_000
	defaultReconnectMaxDelayMs  = 30_000
	defaultReconnectMaxRetries  = 5
	sessionDeleteTimeout        = time.Second
	responseBodyDrainLimitBytes = 64 * 1024
)

// StreamableHTTPReconnectOptions control reconnection of dropped SSE streams
// (the GET stream, and response streams that carry event IDs). Zero fields
// select the defaults.
type StreamableHTTPReconnectOptions struct {
	// InitialDelayMs is the delay before the first reconnection attempt,
	// unless the server sent a `retry` field. Default: 1000.
	InitialDelayMs int
	// MaxDelayMs bounds the exponential backoff. Default: 30000.
	MaxDelayMs int
	// MaxRetries is the number of consecutive failed attempts before giving up
	// on a stream. Default: 5.
	MaxRetries int
}

// StreamableHTTPTransportOptions configure a [StreamableHTTPTransport].
type StreamableHTTPTransportOptions struct {
	URL     string
	Headers map[string]string
	Fetch   McpFetch
	// OpenGetStream opens the server-to-client GET stream after
	// initialization. Default: true.
	OpenGetStream   *bool
	MaxMessageBytes int
	AuthProvider    AuthProvider
	Reconnect       StreamableHTTPReconnectOptions
}

// McpHttpError is a failed HTTP exchange.
type McpHttpError struct {
	Status  int
	Message string
	Body    string
}

// NewMcpHttpError is `new McpHttpError(status, message, body)`.
func NewMcpHttpError(status int, message string, body string) *McpHttpError {
	e := &McpHttpError{Status: status, Message: message, Body: body}
	return e
}

func (e *McpHttpError) Error() string { return e.Message }

// Name is the `name` property, "McpHttpError".
func (e *McpHttpError) Name() string { return "McpHttpError" }

// Cause is the `cause` property. No upstream constructor sets one, so it is always nil.
func (e *McpHttpError) Cause() error { return nil }

// McpAuthRequiredError is a 401 response.
type McpAuthRequiredError struct {
	McpHttpError
	// WWWAuthenticate is the challenge header; HasWWWAuthenticate is false
	// when the response had none.
	WWWAuthenticate    string
	HasWWWAuthenticate bool
}

// NewMcpAuthRequiredError is `new McpAuthRequiredError(response, body)`.
func NewMcpAuthRequiredError(response *http.Response, body string) *McpAuthRequiredError {
	e := &McpAuthRequiredError{McpHttpError: *NewMcpHttpError(401, "MCP server requires authentication", body)}
	if values := response.Header.Values("Www-Authenticate"); len(values) > 0 {
		e.WWWAuthenticate, e.HasWWWAuthenticate = strings.Join(values, ", "), true
	}
	return e
}

// Name is the `name` property, "McpAuthRequiredError".
func (e *McpAuthRequiredError) Name() string { return "McpAuthRequiredError" }

// Unwrap lets errors.As find the *McpHttpError.
func (e *McpAuthRequiredError) Unwrap() error { return &e.McpHttpError }

// McpSessionExpiredError is a 404 response for an established session.
type McpSessionExpiredError struct{ McpHttpError }

// NewMcpSessionExpiredError is `new McpSessionExpiredError(body)`.
func NewMcpSessionExpiredError(body string) *McpSessionExpiredError {
	e := &McpSessionExpiredError{*NewMcpHttpError(404, "MCP session expired", body)}
	return e
}

// Name is the `name` property, "McpSessionExpiredError".
func (e *McpSessionExpiredError) Name() string { return "McpSessionExpiredError" }

// Unwrap lets errors.As find the *McpHttpError.
func (e *McpSessionExpiredError) Unwrap() error { return &e.McpHttpError }

func responseContentType(response *http.Response) string {
	value := response.Header.Get("Content-Type")
	before, _, _ := strings.Cut(value, ";")
	return strings.ToLower(jsstring.Trim(before))
}

// jsSpace is the character class of JavaScript's \s, which RE2's \s is not (U+00A0, U+FEFF and the Unicode spaces are whitespace there).
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// insufficientScope is /(?:^|[\s,])error="?insufficient_scope"?/i. A non-unicode JavaScript RegExp's i flag folds ASCII letters only, so the letters
// are spelled as classes: RE2's (?i) would also fold U+017F into s.
var insufficientScope = lazyregexp.New(`(?:^|[` + jsSpace + `,])[eE][rR][rR][oO][rR]="?[iI][nN][sS][uU][fF][fF][iI][cC][iI][eE][nN][tT]_[sS][cC][oO][pP][eE]"?`)

// needsAuthorization is true for a 401, or a 403 with an `insufficient_scope`
// bearer challenge (step-up authorization).
func needsAuthorization(response *http.Response) bool {
	if response.StatusCode == http.StatusUnauthorized {
		return true
	}
	if response.StatusCode != http.StatusForbidden {
		return false
	}
	return insufficientScope.MatchString(strings.Join(response.Header.Values("Www-Authenticate"), ", "))
}

// isTransientStatus marks statuses worth retrying when a stream fails to (re)open.
func isTransientStatus(status int) bool {
	return status == 408 || status == 429 || status >= 500
}

func discardResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func describeHTTPFailure(status int, body string) string {
	text := jsstring.Trim(body)
	units := utf16.Encode([]rune(text))
	snippet := text
	if len(units) > errorMessageBodyChars {
		snippet = string(utf16.Decode(units[:errorMessageBodyChars-3])) + "..."
	}
	if snippet != "" {
		snippet = ": " + snippet
	}
	return fmt.Sprintf("MCP HTTP request failed with status %d%s", status, snippet)
}

type streamCursor struct {
	lastEventID string
	hasEventID  bool
	retryMs     int
	hasRetry    bool
	// received is whether the stream delivered any event since it was (re)opened.
	received bool
}

// StreamableHTTPTransport implements the MCP Streamable HTTP transport:
// JSON and SSE responses to POSTed messages, sessions, the server-to-client
// GET stream with reconnection, and resumption of dropped response streams
// with Last-Event-ID.
type StreamableHTTPTransport struct {
	TransportEvents
	URL     *url.URL
	options StreamableHTTPTransportOptions
	fetch   McpFetch

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu              sync.Mutex
	started         bool
	closed          bool
	sessionID       string
	protocolVersion string
	// lastToken is the access token of the latest request, which Close reuses instead of asking the auth provider.
	lastToken      string
	getStreamStart bool
	// getStreamWritten reports that the GET stream's first request was written or failed; nil until the stream starts.
	getStreamWritten func()
}

// signalGetStreamWritten calls getStreamWritten once the GET stream has started.
func (t *StreamableHTTPTransport) signalGetStreamWritten() {
	t.mu.Lock()
	signal := t.getStreamWritten
	t.mu.Unlock()
	if signal != nil {
		signal()
	}
}

// NewStreamableHTTPTransport returns a transport for options.URL. It returns
// an error when the URL does not parse.
func NewStreamableHTTPTransport(options StreamableHTTPTransportOptions) (*StreamableHTTPTransport, error) {
	u, err := url.Parse(options.URL)
	if err != nil || u.Scheme == "" || u.Host == "" && u.Opaque == "" {
		return nil, fmt.Errorf("Invalid URL: %s", options.URL)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &StreamableHTTPTransport{URL: u, options: options, fetch: options.Fetch, ctx: ctx, cancel: cancel}
	if t.fetch == nil {
		t.fetch = http.DefaultClient
	}
	return t, nil
}

// Options returns the transport's options.
func (t *StreamableHTTPTransport) Options() StreamableHTTPTransportOptions { return t.options }

// SessionID is the session the server assigned, or "".
func (t *StreamableHTTPTransport) SessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

// Start marks the transport started. It opens no connection.
func (t *StreamableHTTPTransport) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return errors.New("MCP Streamable HTTP transport already started")
	}
	if t.closed {
		return NewMcpConnectionClosedError("")
	}
	t.started = true
	return nil
}

// SetProtocolVersion sets the MCP-Protocol-Version header for later requests.
func (t *StreamableHTTPTransport) SetProtocolVersion(version string) {
	t.mu.Lock()
	t.protocolVersion = version
	t.mu.Unlock()
}

func (t *StreamableHTTPTransport) isOpen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started && !t.closed
}

func (t *StreamableHTTPTransport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// Send POSTs one message. A response to a request arrives as JSON, which is
// delivered before Send returns, or as an SSE stream, which a goroutine
// delivers.
func (t *StreamableHTTPTransport) Send(message JSONRPCMessage) error {
	if !t.isOpen() {
		return NewMcpConnectionClosedError("")
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	response, err := t.authorizedFetch(http.MethodPost, map[string]string{
		"accept": "application/json, text/event-stream", "content-type": "application/json",
	}, body)
	if err != nil {
		return err
	}
	if err := t.checkResponse(response); err != nil {
		return err
	}
	t.captureSession(response)

	if !message.IsRequest() {
		// Notifications and responses are acknowledged with 202 and carry no reply; ignore any body.
		discardResponse(response)
		// The server-to-client stream may only open once the session is initialized.
		if message.IsNotification() && message.Method == "notifications/initialized" {
			t.startGetStream()
		}
		return nil
	}
	if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNoContent {
		discardResponse(response)
		return NewMcpHttpError(response.StatusCode, fmt.Sprintf("MCP server accepted request %s without a response", message.Method), "")
	}
	switch kind := responseContentType(response); kind {
	case "application/json":
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			return err
		}
		// Response.json() decodes the body as UTF-8 without its BOM, then
		// rejects invalid JSON with JSON.parse's SyntaxError.
		data = bytes.TrimPrefix(data, []byte("\uFEFF"))
		if err := jsonparse.Validate(data); err != nil {
			return err
		}
		var items []json.RawMessage
		if isJSONArray(data) {
			if err := json.Unmarshal(data, &items); err != nil {
				return err
			}
		} else {
			items = []json.RawMessage{data}
		}
		for _, item := range items {
			parsed, err := ParseJSONRPCMessage(item)
			if err != nil {
				return err
			}
			t.EmitMessage(parsed)
		}
		return nil
	case "text/event-stream":
		t.wg.Go(func() {
			t.consumeResponseStream(response.Body, *message.ID)
		})
		return nil
	default:
		discardResponse(response)
		if kind == "" {
			kind = "missing"
		}
		return NewMcpHttpError(response.StatusCode, "Unsupported MCP response content type: "+kind, "")
	}
}

// Close aborts every in-flight request and stream, ends the session with a
// DELETE when the server assigned one, and waits for the transport's
// goroutines.
func (t *StreamableHTTPTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	started, sessionID := t.started, t.sessionID
	t.mu.Unlock()
	t.cancel()
	if started && sessionID != "" {
		// Best effort: the session expires on the server anyway. The auth provider may refresh tokens over the
		// network, so it is not asked here and closing never waits for a refresh.
		ctx, cancel := context.WithTimeout(context.Background(), sessionDeleteTimeout)
		// Best effort: the session expires on the server anyway. The auth provider may refresh tokens over the network, so it is not asked here and closing never waits for a refresh (streamable-http.ts close, #10565).
		t.mu.Lock()
		token := t.lastToken
		t.mu.Unlock()
		if request, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.URL.String(), nil); err == nil {
			request.Header = t.buildHeaders(nil, token)
			if response, err := t.fetch.Do(request); err == nil {
				discardResponse(response)
			}
		}
		cancel()
	}
	t.wg.Wait()
	t.EmitClose()
	return nil
}

// authorizedFetch sends a request with auth headers. A 401 (or a 403 asking
// for more scope) is handed to the auth provider once, and the request is
// retried with whatever credentials it left behind.
func (t *StreamableHTTPTransport) authorizedFetch(method string, extra map[string]string, body []byte) (*http.Response, error) {
	var handler UnauthorizedHandler
	if t.options.AuthProvider != nil {
		handler, _ = t.options.AuthProvider.(UnauthorizedHandler)
	}
	for attempt := 0; ; attempt++ {
		headers, token, err := t.headers(t.ctx, extra)
		if err != nil {
			return nil, err
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		request, err := http.NewRequestWithContext(t.ctx, method, t.URL.String(), reader)
		if err != nil {
			return nil, err
		}
		if method == http.MethodGet {
			request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
				WroteRequest: func(httptrace.WroteRequestInfo) { t.signalGetStreamWritten() },
			}))
		}
		request.Header = headers
		response, err := t.fetch.Do(request)
		if err != nil {
			return nil, err
		}
		if attempt > 0 || handler == nil || !needsAuthorization(response) {
			return response, nil
		}
		err = handler.OnUnauthorized(t.ctx, UnauthorizedContext{Response: response, ServerURL: t.URL, Fetch: t.fetch, Token: token})
		discardResponse(response)
		if err != nil {
			return nil, err
		}
	}
}

func (t *StreamableHTTPTransport) headers(ctx context.Context, extra map[string]string) (http.Header, string, error) {
	var token string
	if t.options.AuthProvider != nil {
		var err error
		if token, err = t.options.AuthProvider.Token(ctx); err != nil {
			return nil, "", err
		}
	}
	t.mu.Lock()
	t.lastToken = token
	t.mu.Unlock()
	return t.buildHeaders(extra, token), token, nil
}

func (t *StreamableHTTPTransport) buildHeaders(extra map[string]string, token string) http.Header {
	headers := http.Header{}
	for name, value := range t.options.Headers {
		headers.Set(name, value)
	}
	for name, value := range extra {
		headers.Set(name, value)
	}
	t.mu.Lock()
	sessionID, version := t.sessionID, t.protocolVersion
	t.mu.Unlock()
	if sessionID != "" {
		headers.Set("Mcp-Session-Id", sessionID)
	}
	if version != "" {
		headers.Set("MCP-Protocol-Version", version)
	}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers
}

func (t *StreamableHTTPTransport) captureSession(response *http.Response) {
	if id := response.Header.Get("Mcp-Session-Id"); id != "" {
		t.mu.Lock()
		t.sessionID = id
		t.mu.Unlock()
	}
}

func (t *StreamableHTTPTransport) checkResponse(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	// response.text() decodes the body as UTF-8 without its BOM, and slice
	// keeps MAX_ERROR_BODY_BYTES UTF-16 units, which take at most three bytes
	// each.
	data, _ := io.ReadAll(io.LimitReader(response.Body, int64(3*maxErrorBodyBytes+len("\uFEFF"))))
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, responseBodyDrainLimitBytes))
	_ = response.Body.Close()
	body := jsstring.Slice(jsstring.FromUTF8(bytes.TrimPrefix(data, []byte("\uFEFF"))), 0, maxErrorBodyBytes)
	if response.StatusCode == http.StatusUnauthorized {
		return NewMcpAuthRequiredError(response, body)
	}
	t.mu.Lock()
	hasSession := t.sessionID != ""
	t.mu.Unlock()
	if response.StatusCode == http.StatusNotFound && hasSession {
		return NewMcpSessionExpiredError(body)
	}
	return NewMcpHttpError(response.StatusCode, describeHTTPFailure(response.StatusCode, body), body)
}

func (t *StreamableHTTPTransport) maxMessageBytes() int {
	if t.options.MaxMessageBytes > 0 {
		return t.options.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

func (t *StreamableHTTPTransport) consumeSSE(stream io.Reader, cursor *streamCursor, onMessage func(JSONRPCMessage)) error {
	return ConsumeSSEStream(stream, ConsumeSSEOptions{
		MaxEventBytes: t.maxMessageBytes(),
		OnID: func(id string) {
			cursor.lastEventID, cursor.hasEventID = id, true
		},
		OnRetry: func(delayMs int) {
			cursor.retryMs, cursor.hasRetry = delayMs, true
		},
		OnEvent: func(event SSEEvent) {
			cursor.received = true
			// Events without data prime resumption; other event types are not JSON-RPC.
			if jsstring.Trim(event.Data) == "" || (event.Event != "" && event.Event != "message") {
				return
			}
			message, err := parseWireMessage([]byte(event.Data))
			if err != nil {
				t.EmitError(err)
				return
			}
			if onMessage != nil {
				onMessage(message)
			}
			t.EmitMessage(message)
		},
	})
}

// consumeResponseStream reads the SSE stream answering one request. When the
// stream ends or breaks before the response arrives and the server assigned
// event IDs, it resumes the stream with GET and Last-Event-ID, as the server
// may close response streams at will. Otherwise only this request fails.
func (t *StreamableHTTPTransport) consumeResponseStream(body io.ReadCloser, requestID JSONRPCID) {
	cursor := &streamCursor{}
	answered := false
	onMessage := func(message JSONRPCMessage) {
		if message.IsResponse() && *message.ID == requestID {
			answered = true
		}
	}
	stream := io.ReadCloser(body)
	var failure error
	for attempt := 0; ; {
		if stream != nil {
			failure = t.consumeSSE(stream, cursor, onMessage)
			_ = stream.Close()
			stream = nil
		}
		if answered || t.isClosed() {
			return
		}
		if failure != nil && !t.isRetryable(failure) {
			break
		}
		if !cursor.hasEventID || attempt >= t.maxRetries() {
			break
		}
		if cursor.received {
			attempt = 0
		}
		cursor.received = false
		delay := t.reconnectDelay(attempt, cursor)
		attempt++
		if !t.sleep(delay) {
			return
		}
		var err error
		stream, err = t.openSSEStream(cursor.lastEventID, cursor.hasEventID)
		if err != nil {
			failure = err
			if !t.isRetryable(err) {
				break
			}
			stream = nil
		}
	}
	if t.isClosed() {
		return
	}
	reason := "stream ended without a response"
	if failure != nil {
		reason = failure.Error()
	}
	t.EmitMessage(NewErrorResponse(requestID, JSONRPCErrorCodes.InternalError, "MCP response stream failed: "+reason, nil))
}

func (t *StreamableHTTPTransport) startGetStream() {
	if (t.options.OpenGetStream != nil && !*t.options.OpenGetStream) || t.isClosed() {
		return
	}
	t.mu.Lock()
	if t.getStreamStart || t.closed {
		t.mu.Unlock()
		return
	}
	t.getStreamStart = true
	t.wg.Add(1)
	written := make(chan struct{})
	var once sync.Once
	t.getStreamWritten = func() { once.Do(func() { close(written) }) }
	t.mu.Unlock()
	go func() {
		defer t.wg.Done()
		defer t.signalGetStreamWritten()
		t.runGetStream()
	}()
	// The upstream transport issues the GET request before send returns, so it reaches the server before the client's
	// next request. A goroutine can start late: wait until the first attempt has written its request or failed. Another
	// McpFetch may not report its writes and is not waited for.
	if _, ok := t.fetch.(*http.Client); ok {
		select {
		case <-written:
		case <-t.ctx.Done():
		}
	}
}

// runGetStream keeps the server-to-client stream open, reconnecting with
// backoff when it drops.
func (t *StreamableHTTPTransport) runGetStream() {
	cursor := &streamCursor{}
	for attempt := 0; !t.isClosed(); {
		stream, err := t.openSSEStream(cursor.lastEventID, cursor.hasEventID)
		t.signalGetStreamWritten()
		if err == nil {
			// The server does not offer a GET stream.
			if stream == nil {
				return
			}
			openedAt := time.Now()
			err = t.consumeSSE(stream, cursor, nil)
			_ = stream.Close()
			// A stream that stayed up for a while counts as healthy, even if it was idle.
			if err == nil && (cursor.received || time.Since(openedAt) > time.Duration(t.maxDelay())*time.Millisecond) {
				attempt = 0
			}
		}
		if err != nil {
			if t.isClosed() {
				return
			}
			if !t.isRetryable(err) {
				t.EmitError(err)
				return
			}
		}
		cursor.received = false
		if attempt >= t.maxRetries() {
			t.EmitError(errors.New("MCP server-to-client stream dropped and could not be reopened"))
			return
		}
		delay := t.reconnectDelay(attempt, cursor)
		attempt++
		if !t.sleep(delay) {
			return
		}
	}
}

// openSSEStream opens a GET SSE stream. It returns a nil stream when the
// server answers 405 (no GET stream).
func (t *StreamableHTTPTransport) openSSEStream(lastEventID string, hasLastEventID bool) (io.ReadCloser, error) {
	extra := map[string]string{"accept": "text/event-stream"}
	if hasLastEventID {
		extra["last-event-id"] = lastEventID
	}
	response, err := t.authorizedFetch(http.MethodGet, extra, nil)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusMethodNotAllowed {
		discardResponse(response)
		return nil, nil
	}
	if err := t.checkResponse(response); err != nil {
		return nil, err
	}
	t.captureSession(response)
	kind := responseContentType(response)
	if kind != "text/event-stream" || response.Body == nil {
		discardResponse(response)
		if kind == "" {
			kind = "missing"
		}
		return nil, NewMcpHttpError(response.StatusCode, "Unsupported MCP GET response content type: "+kind, "")
	}
	return response.Body, nil
}

// isRetryable: network failures and transient statuses are retried; auth,
// session, and protocol errors are not.
func (t *StreamableHTTPTransport) isRetryable(err error) bool {
	if httpErr, ok := errors.AsType[*McpHttpError](err); ok {
		return isTransientStatus(httpErr.Status)
	}
	var tooLarge *sseTooLargeError
	var mcpErr *McpError
	if errors.As(err, &tooLarge) || errors.As(err, &mcpErr) || errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	var urlErr *url.Error
	return errors.As(err, &netErr) || errors.As(err, &urlErr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func (t *StreamableHTTPTransport) reconnectDelay(attempt int, cursor *streamCursor) time.Duration {
	if cursor.hasRetry {
		return time.Duration(cursor.retryMs) * time.Millisecond
	}
	initial := t.options.Reconnect.InitialDelayMs
	if initial == 0 {
		initial = defaultReconnectInitialMs
	}
	delay := float64(initial)
	for range attempt {
		delay *= 2
		if delay > float64(t.maxDelay()) {
			break
		}
	}
	return time.Duration(min(delay, float64(t.maxDelay()))) * time.Millisecond
}

func (t *StreamableHTTPTransport) maxDelay() int {
	if t.options.Reconnect.MaxDelayMs != 0 {
		return t.options.Reconnect.MaxDelayMs
	}
	return defaultReconnectMaxDelayMs
}

func (t *StreamableHTTPTransport) maxRetries() int {
	if t.options.Reconnect.MaxRetries != 0 {
		return t.options.Reconnect.MaxRetries
	}
	return defaultReconnectMaxRetries
}

// sleep reports false when the transport closed while waiting.
func (t *StreamableHTTPTransport) sleep(d time.Duration) bool {
	if t.ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-t.ctx.Done():
		return false
	}
}
