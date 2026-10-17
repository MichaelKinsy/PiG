package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

const (
	codexWebSocketBeta           = "responses_websockets=2026-02-06"
	codexWebSocketConnectTimeout = 15 * time.Second
	codexWebSocketCacheTTL       = 5 * time.Minute
	codexWebSocketMaxAge         = 55 * time.Minute
)

// codexWebSocketError is a WebSocket transport failure as Node's WebSocket reports it to openai-codex-responses.ts: extractWebSocketError's "WebSocket error" for a failed handshake or a dropped connection, and extractWebSocketCloseError's WebSocketCloseError, with the close code, for a close frame (openai-codex-responses.ts:1250-1288).
type codexWebSocketError struct {
	name    string
	message string
	code    *int
	cause   error
}

func (e *codexWebSocketError) Error() string { return e.message }
func (e *codexWebSocketError) Name() string  { return e.name }
func (e *codexWebSocketError) Unwrap() error { return e.cause }

// DiagnosticCode is WebSocketCloseError's `code`, the close code.
func (e *codexWebSocketError) DiagnosticCode() any {
	if e.code == nil {
		return nil
	}
	return *e.code
}

// nodeWebSocketError maps a gorilla dial, write or read failure to the error Node's WebSocket gives. Node fires `error` before `close` for a connection that ends without a close frame (1006), so only a received close frame is a WebSocketCloseError.
func nodeWebSocketError(err error) error {
	if closeError, ok := errors.AsType[*websocket.CloseError](err); ok && closeError.Code != websocket.CloseAbnormalClosure {
		code := closeError.Code
		message := "WebSocket closed " + strconv.Itoa(code)
		if closeError.Text != "" {
			message += " " + closeError.Text
		} else if code == websocket.CloseMessageTooBig {
			message += " message too big"
		}
		return &codexWebSocketError{name: "WebSocketCloseError", message: message, code: &code, cause: err}
	}
	return &codexWebSocketError{name: "Error", message: "WebSocket error", cause: err}
}

// codexAPIError is CodexApiError, the error mapCodexEvents throws for an `error` event (openai-codex-responses.ts:682-693).
type codexAPIError struct {
	message string
	code    string
}

func (e *codexAPIError) Error() string { return e.message }
func (e *codexAPIError) Name() string  { return "CodexApiError" }
func (e *codexAPIError) DiagnosticCode() any {
	if e.code == "" {
		return nil
	}
	return e.code
}

// codexWebSocketTransportFailure marks a read failure after the message stream started, so the parser records the transport failure before it ends the message.
type codexWebSocketTransportFailure struct{ err error }

func (e *codexWebSocketTransportFailure) Error() string { return e.err.Error() }
func (e *codexWebSocketTransportFailure) Unwrap() error { return e.err }

// codexTransportFailureDiagnostic is the provider_transport_failure diagnostic openai-codex-responses.ts:356-365 appends when the WebSocket transport fails: before the message stream started the request falls back to SSE, after it the error ends the message.
func codexTransportFailureDiagnostic(failure error, transport Transport, started bool, requestBytes int) AssistantMessageDiagnostic {
	configured := string(transport)
	if configured == "" {
		configured = string(TransportAuto)
	}
	phase := "before_message_stream_start"
	if started {
		phase = "after_message_stream_start"
	}
	details := newDiagnosticDetails()
	details.set("configuredTransport", configured, nil)
	if !started {
		details.set("fallbackTransport", "sse", nil)
	}
	details.set("eventsEmitted", started, nil)
	details.set("phase", phase, nil)
	details.set("requestBytes", requestBytes, nil)
	return details.attach(CreateAssistantMessageDiagnostic("provider_transport_failure", failure, nil))
}

// codexTransportFailureReader appends the after-start transport failure diagnostic to the message, on the parser's goroutine, when the WebSocket feed fails.
type codexTransportFailureReader struct {
	io.Reader
	ctx          context.Context
	builder      *assistantStreamBuilder
	transport    Transport
	requestBytes int
	recorded     bool
}

func (r *codexTransportFailureReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if failure, ok := errors.AsType[*codexWebSocketTransportFailure](err); ok && !r.recorded && r.ctx.Err() == nil {
		r.recorded = true
		AppendAssistantMessageDiagnostic(r.builder.partial, codexTransportFailureDiagnostic(failure.err, r.transport, true, r.requestBytes))
		r.builder.touch()
	}
	return n, err
}

// OpenAICodexWebSocketDebugStats reports connection and continuation behavior
// for one logical Codex session.
type OpenAICodexWebSocketDebugStats struct {
	Requests                int
	ConnectionsCreated      int
	ConnectionsReused       int
	CachedContextRequests   int
	StoreTrueRequests       int
	FullContextRequests     int
	DeltaRequests           int
	LastInputItems          int
	LastDeltaInputItems     *int
	LastPreviousResponseId  string
	WebSocketFailures       int
	SSEFallbacks            int
	WebSocketFallbackActive bool
	LastWebSocketError      string
}

// codexWebSocketContinuation is what the next request on the connection is compared with (openai-codex-responses.ts
// getCachedWebSocketInputDelta). Pi compares JSON.stringify texts, so the previous request is kept as the text of its members and
// input items, not as a decoded tree.
type codexWebSocketContinuation struct {
	// lastRest is the previous request's members other than input and previous_response_id, in its order.
	lastRest       []jsonMember
	lastResponseID string
	// baseline is the previous request's input items, then the items of its response.
	baseline []json.RawMessage
}

type codexWebSocketEntry struct {
	connection   *websocket.Conn
	busy         bool
	finishing    bool
	released     chan struct{}
	createdAt    time.Time
	idleTimer    *time.Timer
	continuation *codexWebSocketContinuation
}

type codexWebSocketStore struct {
	mu       sync.Mutex
	sessions map[string]map[string]*codexWebSocketEntry
	stats    map[string]*OpenAICodexWebSocketDebugStats
	fallback map[string]bool
}

var codexWebSocketSessions = codexWebSocketStore{
	sessions: make(map[string]map[string]*codexWebSocketEntry),
	stats:    make(map[string]*OpenAICodexWebSocketDebugStats),
	fallback: make(map[string]bool),
}

func init() {
	RegisterSessionResourceCleanup(func(sessionID string) {
		CloseOpenAICodexWebSocketSessions(sessionID)
	})
}

// GetOpenAICodexWebSocketDebugStats returns a copy of one session's counters.
func GetOpenAICodexWebSocketDebugStats(sessionID string) *OpenAICodexWebSocketDebugStats {
	codexWebSocketSessions.mu.Lock()
	defer codexWebSocketSessions.mu.Unlock()
	stats := codexWebSocketSessions.stats[sessionID]
	if stats == nil {
		return nil
	}
	copy := *stats
	if stats.LastDeltaInputItems != nil {
		copy.LastDeltaInputItems = new(*stats.LastDeltaInputItems)
	}
	return &copy
}

// ResetOpenAICodexWebSocketDebugStats clears one session's counters, or all
// counters when no session id is supplied.
func ResetOpenAICodexWebSocketDebugStats(sessionID ...string) {
	codexWebSocketSessions.mu.Lock()
	defer codexWebSocketSessions.mu.Unlock()
	if len(sessionID) > 0 && sessionID[0] != "" {
		delete(codexWebSocketSessions.stats, sessionID[0])
		delete(codexWebSocketSessions.fallback, sessionID[0])
		return
	}
	clear(codexWebSocketSessions.stats)
	clear(codexWebSocketSessions.fallback)
}

// CloseOpenAICodexWebSocketSessions closes one session's cached connections,
// or every cached connection when no session id is supplied.
func CloseOpenAICodexWebSocketSessions(sessionID ...string) {
	codexWebSocketSessions.mu.Lock()
	var entries []*codexWebSocketEntry
	if len(sessionID) > 0 && sessionID[0] != "" {
		for _, entry := range codexWebSocketSessions.sessions[sessionID[0]] {
			entries = append(entries, entry)
		}
		delete(codexWebSocketSessions.sessions, sessionID[0])
	} else {
		for _, accounts := range codexWebSocketSessions.sessions {
			for _, entry := range accounts {
				entries = append(entries, entry)
			}
		}
		clear(codexWebSocketSessions.sessions)
	}
	for _, entry := range entries {
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
		}
	}
	codexWebSocketSessions.mu.Unlock()
	for _, entry := range entries {
		closeCodexWebSocket(entry.connection, "debug_close")
	}
}

func codexDebugStatsLocked(sessionID string) *OpenAICodexWebSocketDebugStats {
	stats := codexWebSocketSessions.stats[sessionID]
	if stats == nil {
		stats = &OpenAICodexWebSocketDebugStats{}
		codexWebSocketSessions.stats[sessionID] = stats
	}
	return stats
}

func recordCodexWebSocketFailure(sessionID string, failure error) {
	if sessionID == "" || failure == nil {
		return
	}
	codexWebSocketSessions.mu.Lock()
	defer codexWebSocketSessions.mu.Unlock()
	stats := codexDebugStatsLocked(sessionID)
	codexWebSocketSessions.fallback[sessionID] = true
	stats.WebSocketFailures++
	stats.WebSocketFallbackActive = true
	stats.LastWebSocketError = failure.Error()
}

func recordCodexWebSocketSSEFallback(sessionID string) {
	if sessionID == "" {
		return
	}
	codexWebSocketSessions.mu.Lock()
	defer codexWebSocketSessions.mu.Unlock()
	stats := codexDebugStatsLocked(sessionID)
	stats.SSEFallbacks++
	stats.WebSocketFallbackActive = codexWebSocketSessions.fallback[sessionID]
}

func codexWebSocketFallbackActive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	codexWebSocketSessions.mu.Lock()
	defer codexWebSocketSessions.mu.Unlock()
	return codexWebSocketSessions.fallback[sessionID]
}

// resolveCodexWebSocketURL is resolveCodexWebSocketUrl for an endpoint that resolveCodexURL has produced: `new URL(endpoint)` with https: switched to wss: and http: to ws:. Pi's WebSocket constructor rejects any other
// scheme when it connects; the dial here fails earlier with an unsupported-scheme error.
func resolveCodexWebSocketURL(endpoint string) (string, error) {
	href, err := codexWebSocketHref(endpoint)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(href, "wss:") || strings.HasPrefix(href, "ws:") {
		return href, nil
	}
	scheme, _, _ := strings.Cut(href, ":")
	return "", fmt.Errorf("openai-codex-responses: unsupported WebSocket URL scheme %q", scheme)
}

// codexWebSocketHref is `new URL(endpoint)` with the protocol setter applied for https: and http:, and serialized. Its error is the constructor's TypeError message, "Invalid URL".
func codexWebSocketHref(endpoint string) (string, error) {
	parsed, err := nodeurl.ParseURL(endpoint)
	if err != nil {
		return "", err
	}
	href := parsed.Href()
	switch parsed.Protocol {
	case "https:":
		return "wss:" + strings.TrimPrefix(href, "https:"), nil
	case "http:":
		return "ws:" + strings.TrimPrefix(href, "http:"), nil
	}
	return href, nil
}

func codexWebSocketHeaders(base map[string]string, options ProviderHeaders, token, accountID, requestID string) http.Header {
	headers := make(http.Header, len(base)+7)
	// Defaults first so model and caller headers override them (buildBaseCodexHeaders, #10429).
	// pig divergence (D26): PiG names itself as the Codex originator.
	headers.Set("originator", pigidentity.CodexOriginator)
	// pig divergence (D65): the default User-Agent is PiG's product identity.
	headers.Set("User-Agent", PiUserAgent())
	for name, value := range base {
		headers.Set(name, value)
	}
	for name, value := range options {
		if value == nil {
			headers.Del(name)
		} else {
			headers.Set(name, *value)
		}
	}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("chatgpt-account-id", accountID)
	headers.Set("OpenAI-Beta", codexWebSocketBeta)
	headers.Set("x-client-request-id", requestID)
	headers.Set("session-id", requestID)
	return headers
}

type acquiredCodexWebSocket struct {
	connection *websocket.Conn
	entry      *codexWebSocketEntry
	reused     bool
	sessionID  string
	accountID  string
}

func optionTimeout(value *int) time.Duration {
	if value == nil {
		return 0
	}
	return time.Duration(*value) * time.Millisecond
}

func acquireCodexWebSocket(ctx context.Context, endpoint string, headers http.Header, sessionID, accountID string, connectTimeout time.Duration) (*acquiredCodexWebSocket, error) {
	cacheNewConnection := sessionID != ""
	if sessionID != "" {
		codexWebSocketSessions.mu.Lock()
		accounts := codexWebSocketSessions.sessions[sessionID]
		entry := accounts[accountID]
		if entry != nil && !entry.busy && time.Since(entry.createdAt) < codexWebSocketMaxAge {
			if entry.idleTimer != nil {
				entry.idleTimer.Stop()
				entry.idleTimer = nil
			}
			entry.busy = true
			entry.finishing = false
			entry.released = make(chan struct{})
			codexWebSocketSessions.mu.Unlock()
			return &acquiredCodexWebSocket{connection: entry.connection, entry: entry, reused: true, sessionID: sessionID, accountID: accountID}, nil
		}
		if entry != nil && entry.busy && entry.finishing {
			// Result publication can wake a sequential caller just before the
			// parser stores continuation state and releases the socket. Wait only
			// for that terminal handoff; genuinely concurrent active requests use
			// a separate one-shot connection below.
			released := entry.released
			codexWebSocketSessions.mu.Unlock()
			select {
			case <-released:
				return acquireCodexWebSocket(ctx, endpoint, headers, sessionID, accountID, connectTimeout)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if entry != nil && entry.busy {
			cacheNewConnection = false
		}
		if entry != nil && !entry.busy {
			delete(accounts, accountID)
			if len(accounts) == 0 {
				delete(codexWebSocketSessions.sessions, sessionID)
			}
			codexWebSocketSessions.mu.Unlock()
			closeCodexWebSocket(entry.connection, "connection_age_limit")
		} else {
			codexWebSocketSessions.mu.Unlock()
		}
	}

	webSocketURL, err := resolveCodexWebSocketURL(endpoint)
	if err != nil {
		return nil, err
	}
	var connectionMu sync.Mutex
	var dialingConnection net.Conn
	dialer := websocket.Dialer{Proxy: proxyFromEnvironment, HandshakeTimeout: connectTimeout}
	dialer.NetDialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		connection, dialErr := (&net.Dialer{}).DialContext(dialCtx, network, address)
		connectionMu.Lock()
		dialingConnection = connection
		if connection != nil && ctx.Err() != nil {
			_ = connection.SetDeadline(time.Now())
		}
		connectionMu.Unlock()
		return connection, dialErr
	}
	stopAbort := context.AfterFunc(ctx, func() {
		connectionMu.Lock()
		if dialingConnection != nil {
			_ = dialingConnection.SetDeadline(time.Now())
		}
		connectionMu.Unlock()
	})
	connection, response, err := dialer.DialContext(ctx, webSocketURL, headers)
	stopAbort()
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if ctx.Err() != nil {
		if connection != nil {
			_ = connection.Close()
		}
		return nil, ctx.Err()
	}
	if err != nil {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, fmt.Errorf("WebSocket connect timeout after %dms", connectTimeout.Milliseconds())
		}
		return nil, nodeWebSocketError(err)
	}
	acquired := &acquiredCodexWebSocket{connection: connection, sessionID: sessionID, accountID: accountID}
	if !cacheNewConnection {
		return acquired, nil
	}
	entry := &codexWebSocketEntry{connection: connection, busy: true, released: make(chan struct{}), createdAt: time.Now()}
	acquired.entry = entry
	codexWebSocketSessions.mu.Lock()
	accounts := codexWebSocketSessions.sessions[sessionID]
	if accounts == nil {
		accounts = make(map[string]*codexWebSocketEntry)
		codexWebSocketSessions.sessions[sessionID] = accounts
	}
	accounts[accountID] = entry
	codexWebSocketSessions.mu.Unlock()
	return acquired, nil
}

func releaseCodexWebSocketWaiter(entry *codexWebSocketEntry) {
	if entry.released == nil {
		return
	}
	close(entry.released)
	entry.released = nil
}

func (acquired *acquiredCodexWebSocket) release(keep bool) {
	if acquired.entry == nil || acquired.sessionID == "" {
		closeCodexWebSocket(acquired.connection, "done")
		return
	}
	codexWebSocketSessions.mu.Lock()
	accounts := codexWebSocketSessions.sessions[acquired.sessionID]
	if accounts[acquired.accountID] != acquired.entry || !keep {
		if accounts[acquired.accountID] == acquired.entry {
			delete(accounts, acquired.accountID)
			if len(accounts) == 0 {
				delete(codexWebSocketSessions.sessions, acquired.sessionID)
			}
		}
		releaseCodexWebSocketWaiter(acquired.entry)
		codexWebSocketSessions.mu.Unlock()
		closeCodexWebSocket(acquired.connection, "done")
		return
	}
	acquired.entry.busy = false
	acquired.entry.finishing = false
	releaseCodexWebSocketWaiter(acquired.entry)
	entry := acquired.entry
	sessionID := acquired.sessionID
	accountID := acquired.accountID
	entry.idleTimer = time.AfterFunc(codexWebSocketCacheTTL, func() {
		codexWebSocketSessions.mu.Lock()
		accounts := codexWebSocketSessions.sessions[sessionID]
		if accounts[accountID] == entry && !entry.busy {
			delete(accounts, accountID)
			if len(accounts) == 0 {
				delete(codexWebSocketSessions.sessions, sessionID)
			}
			codexWebSocketSessions.mu.Unlock()
			closeCodexWebSocket(entry.connection, "idle_timeout")
			return
		}
		codexWebSocketSessions.mu.Unlock()
	})
	codexWebSocketSessions.mu.Unlock()
}

func readCodexWebSocket(ctx context.Context, connection *websocket.Conn, timeout time.Duration) (int, []byte, error) {
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	_ = connection.SetReadDeadline(deadline)
	stopAbort := context.AfterFunc(ctx, func() {
		_ = connection.SetReadDeadline(time.Now())
	})
	messageType, data, err := connection.ReadMessage()
	stopAbort()
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return 0, nil, fmt.Errorf("WebSocket idle timeout after %dms", timeout.Milliseconds())
	}
	if err != nil {
		return 0, nil, nodeWebSocketError(err)
	}
	return messageType, data, nil
}

func closeCodexWebSocket(connection *websocket.Conn, reason string) {
	if connection == nil {
		return
	}
	deadline := time.Now().Add(time.Second)
	_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason), deadline)
	_ = connection.Close()
}

// codexRequest is a Codex request body read as the members of its text. The body is JSON.stringify text (marshalRequestBody or a
// replacement's canonical form), so the text of a member is JSON.stringify of its value.
type codexRequest struct {
	// members is the body's members in the order the text has them; nil for a null body.
	members []jsonMember
	// input is the items of the input member; inputArray is false when the body has an input that is neither an array nor null.
	input      []json.RawMessage
	inputArray bool
}

// readCodexRequest reads the body the WebSocket sends. A body that is not a JSON object or null fails as decoding it into an
// object does.
func readCodexRequest(body []byte) (codexRequest, error) {
	request := codexRequest{inputArray: true}
	if trimmed := bytes.TrimSpace(body); string(trimmed) == "null" {
		return request, nil
	} else if len(trimmed) == 0 || trimmed[0] != '{' {
		var object map[string]any
		err := json.Unmarshal(body, &object)
		if err == nil {
			err = errors.New("not a JSON object")
		}
		return codexRequest{}, err
	}
	members, err := readJSONObjectMembers(body)
	if err != nil {
		var object map[string]any
		if decodeErr := json.Unmarshal(body, &object); decodeErr != nil {
			return codexRequest{}, decodeErr
		}
		return codexRequest{}, err
	}
	request.members = members
	if value, ok := request.member("input"); ok && string(value) != "null" {
		if json.Unmarshal(value, &request.input) != nil {
			request.input, request.inputArray = nil, false
		}
	}
	return request, nil
}

// member returns the text of the body's member key.
func (request codexRequest) member(key string) (json.RawMessage, bool) {
	for _, member := range request.members {
		if member.key == key {
			return member.value, true
		}
	}
	return nil, false
}

// rest is requestBodyWithoutInput: the members other than input and previous_response_id.
func (request codexRequest) rest() []jsonMember {
	rest := make([]jsonMember, 0, len(request.members))
	for _, member := range request.members {
		if member.key != "input" && member.key != "previous_response_id" {
			rest = append(rest, member)
		}
	}
	return rest
}

// codexDelta is a request continued from the previous response: previous_response_id names it, and input is the last count items.
type codexDelta struct {
	previousResponseID string
	count              int
}

// cachedCodexDelta is buildCachedWebSocketRequestBody (openai-codex-responses.ts): the delta of a request whose members other than
// input equal the previous request's and whose input starts with the previous input and response items, compared as
// JSON.stringify texts. Any other request ends the continuation and is sent whole (nil).
func cachedCodexDelta(entry *codexWebSocketEntry, request codexRequest) *codexDelta {
	if entry == nil || entry.continuation == nil {
		return nil
	}
	continuation := entry.continuation
	if !sameJSONMembers(request.rest(), continuation.lastRest) || !request.inputArray || len(request.input) < len(continuation.baseline) {
		entry.continuation = nil
		return nil
	}
	for index, item := range continuation.baseline {
		if !bytes.Equal(request.input[index], item) {
			entry.continuation = nil
			return nil
		}
	}
	return &codexDelta{previousResponseID: continuation.lastResponseID, count: len(request.input) - len(continuation.baseline)}
}

// sameJSONMembers reports whether two member lists stringify alike: the same keys in the same order with the same texts.
func sameJSONMembers(a, b []jsonMember) bool {
	return slices.EqualFunc(a, b, func(x, y jsonMember) bool { return x.key == y.key && bytes.Equal(x.value, y.value) })
}

// codexWebSocketMessage is JSON.stringify({ type: "response.create", ...requestBody }) (openai-codex-responses.ts:1542): type first, with the body's own type value
// when it has one, then the members of the request body in the order the body text has them. requestBody is the body after the cached continuation
// (cachedCodexDelta): a continuation replaces input with its tail and sets previous_response_id, as `{ ...body, previous_response_id, input: delta }`
// does, in place for a member the body has and last for one it lacks. Without one, a previous_response_id the body has is written as it decodes.
func codexWebSocketMessage(request codexRequest, delta *codexDelta) ([]byte, error) {
	out := make([]jsonMember, 1, len(request.members)+3)
	out[0] = jsonMember{key: "type", value: json.RawMessage(`"response.create"`)}
	hasPrevious, hasInput := delta != nil, delta != nil
	for _, member := range request.members {
		switch member.key {
		case "type":
			out[0].value = member.value
			continue
		case "previous_response_id":
			var previousID any
			if hasPrevious {
				previousID, hasPrevious = delta.previousResponseID, false
			} else if err := json.Unmarshal(member.value, &previousID); err != nil {
				return nil, err
			}
			encoded, err := marshalRequestBody(previousID)
			if err != nil {
				return nil, err
			}
			member.value = encoded
		case "input":
			if hasInput {
				hasInput = false
				// The delta is a tail of the body's input items; its items keep the body's text.
				if items := request.input; delta.count < len(items) {
					member.value = joinJSONArray(items[len(items)-delta.count:])
				}
			}
		}
		out = append(out, member)
	}
	if hasPrevious {
		encoded, err := marshalRequestBody(delta.previousResponseID)
		if err != nil {
			return nil, err
		}
		out = append(out, jsonMember{key: "previous_response_id", value: encoded})
	}
	if hasInput {
		// A body without input continues with `input: []` (body.input ?? []).
		out = append(out, jsonMember{key: "input", value: json.RawMessage("[]")})
	}
	return writeJSONObjectMembers(out)
}

// joinJSONArray is the JSON array of items' texts.
func joinJSONArray(items []json.RawMessage) json.RawMessage {
	size := 2
	for _, item := range items {
		size += len(item) + 1
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for index, item := range items {
		if index > 0 {
			out = append(out, ',')
		}
		out = append(out, item...)
	}
	return append(out, ']')
}

// startCodexWebSocket streams the request over the Codex WebSocket. A nil stream with a nil error falls back to SSE; fallback is the transport failure that caused it, or nil when the session's WebSocket is already disabled.
func (p *openAIResponsesProvider) startCodexWebSocket(ctx context.Context, endpoint string, body []byte, apiKey, accountID, cacheSessionID string, grammarProps map[string]string, opts StreamOptions) (stream *AssistantMessageEventStream, fallback, err error) {
	return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, true, true)
}

func (p *openAIResponsesProvider) startCodexWebSocketAttempt(ctx context.Context, endpoint string, body []byte, apiKey, accountID, cacheSessionID string, grammarProps map[string]string, opts StreamOptions, retryConnectionLimit, retryMissingContinuation bool) (stream *AssistantMessageEventStream, fallback, err error) {
	if codexWebSocketFallbackActive(cacheSessionID) {
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil, nil
	}
	request, err := readCodexRequest(body)
	if err != nil {
		return nil, nil, fmt.Errorf("openai-codex-responses: WebSocket payload: %w", err)
	}
	requestID := ClampOpenAIPromptCacheKey(cacheSessionID)
	if requestID == "" {
		// upstream: packages/ai/src/api/openai-codex-responses.ts: websocketRequestId = codexSessionId || uuidv7()
		id, err := UUIDv7(nil)
		if err != nil {
			return nil, nil, err
		}
		requestID = id
	}
	headers := codexWebSocketHeaders(p.cfg.ExtraHeaders, opts.Headers, apiKey, accountID, requestID)
	connectTimeout := codexWebSocketConnectTimeout
	if opts.WebSocketConnectTimeoutMs != nil {
		connectTimeout = time.Duration(*opts.WebSocketConnectTimeoutMs) * time.Millisecond
	}
	acquired, err := acquireCodexWebSocket(ctx, endpoint, headers, cacheSessionID, accountID, connectTimeout)
	if err != nil {
		if ctx.Err() != nil {
			// connectWebSocket's onAbort rejects with "Request was aborted" (openai-codex-responses.ts:1134-1136, 1148-1150), and an aborted
			// WebSocket attempt is rethrown, not retried over SSE (:343-355).
			return nil, nil, errCodexAborted
		}
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, err, nil
	}
	// Empty transport has upstream's default "auto" behavior, including cached
	// continuation when a cache session id is available.
	useCachedContext := opts.Transport == TransportWebSocketCached || opts.Transport == TransportAuto || opts.Transport == ""
	var delta *codexDelta
	if useCachedContext {
		delta = cachedCodexDelta(acquired.entry, request)
	}
	encoded, err := codexWebSocketMessage(request, delta)
	if err != nil {
		acquired.release(false)
		return nil, nil, err
	}
	if writeErr := acquired.connection.WriteMessage(websocket.TextMessage, encoded); writeErr != nil {
		err := nodeWebSocketError(writeErr)
		wasReused := acquired.reused
		acquired.release(false)
		if ctx.Err() != nil {
			return nil, nil, errCodexAborted
		}
		if wasReused {
			return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, retryConnectionLimit, retryMissingContinuation)
		}
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, err, nil
	}

	if cacheSessionID != "" {
		codexWebSocketSessions.mu.Lock()
		stats := codexDebugStatsLocked(cacheSessionID)
		stats.Requests++
		if acquired.reused {
			stats.ConnectionsReused++
		} else {
			stats.ConnectionsCreated++
		}
		if useCachedContext {
			stats.CachedContextRequests++
		}
		if store, _ := request.member("store"); string(store) == "true" {
			stats.StoreTrueRequests++
		}
		inputItems, previous := len(request.input), ""
		if delta != nil {
			inputItems, previous = delta.count, delta.previousResponseID
		} else if raw, ok := request.member("previous_response_id"); ok {
			_ = json.Unmarshal(raw, &previous)
		}
		stats.LastInputItems = inputItems
		if previous != "" {
			stats.DeltaRequests++
			count := inputItems
			stats.LastDeltaInputItems = &count
			stats.LastPreviousResponseId = previous
		} else {
			stats.FullContextRequests++
			stats.LastDeltaInputItems = nil
			stats.LastPreviousResponseId = ""
		}
		codexWebSocketSessions.mu.Unlock()
	}

	builder := newAssistantStreamBuilder(ctx, APIOpenAICodexResponses, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	builder.requestServiceTier = requestServiceTierOf(opts)
	builder.setProviderEventObserver(opts, providerEventModel(p.cfg.ModelMetadata, APIOpenAICodexResponses, p.cfg.ProviderID, p.cfg.Model))
	var messageType int
	var first []byte
	for {
		messageType, first, err = readCodexWebSocket(ctx, acquired.connection, optionTimeout(opts.TimeoutMs))
		if err != nil {
			break
		}
		if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
			// mapCodexEvents observes every event, including one that ends this attempt with a retry. A failing observer is not a transport
			// failure, so it ends the request without a retry or SSE fallback (openai-codex-responses.ts:749-755, ProviderStreamEventCallbackError).
			if observeErr := builder.observeCodexFrame(first); observeErr != nil {
				acquired.release(false)
				builder.failUnfinished(StopReasonError, observeErr)
				return builder.stream, nil, nil
			}
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(first, &event) != nil || event.Type != "codex.rate_limits" {
			break
		}
	}
	if err != nil {
		wasReused := acquired.reused
		acquired.release(false)
		if ctx.Err() != nil {
			return nil, nil, errCodexAborted
		}
		if wasReused {
			return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, retryConnectionLimit, retryMissingContinuation)
		}
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, err, nil
	}
	if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
		acquired.release(false)
		err := fmt.Errorf("openai-codex-responses: unexpected WebSocket message type %d", messageType)
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, err, nil
	}
	code := codexWebSocketEventErrorCode(first)
	if code == "previous_response_not_found" && retryMissingContinuation {
		if acquired.entry != nil {
			acquired.entry.continuation = nil
		}
		acquired.release(false)
		return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, retryConnectionLimit, false)
	}
	if code == "websocket_connection_limit_reached" {
		acquired.release(false)
		if retryConnectionLimit {
			return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, false, retryMissingContinuation)
		}
		message := "Codex error: websocket_connection_limit_reached"
		if _, mapped := mapCodexWebSocketEventFrame(first); mapped != nil {
			message = mapped.Error()
		}
		eventErr := &codexAPIError{message: message, code: code}
		recordCodexWebSocketFailure(cacheSessionID, eventErr)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, eventErr, nil
	}

	go p.consumeCodexWebSocket(ctx, acquired, first, request, len(body), grammarProps, useCachedContext, builder, opts)
	return builder.stream, nil, nil
}

func markCodexWebSocketFinishing(entry *codexWebSocketEntry) {
	if entry == nil {
		return
	}
	codexWebSocketSessions.mu.Lock()
	entry.finishing = true
	codexWebSocketSessions.mu.Unlock()
}

func (p *openAIResponsesProvider) consumeCodexWebSocket(ctx context.Context, acquired *acquiredCodexWebSocket, first []byte, request codexRequest, requestBytes int, grammarProps map[string]string, useCachedContext bool, builder *assistantStreamBuilder, opts StreamOptions) {
	reader, writer := io.Pipe()
	feedDone := make(chan struct{})
	parseDone := make(chan struct{})
	// pull carries one token per record the parser asks for. mapCodexEvents observes an event only when processResponsesStream pulls it, after the previous event is
	// normalized (openai-codex-responses.ts:749-751), so the feeder holds each frame's observation until the parser has finished the frame before it.
	pull := make(chan struct{}, 1)
	go func() {
		defer close(feedDone)
		defer func() { _ = writer.Close() }()
		stopCancel := context.AfterFunc(ctx, func() {
			closeCodexWebSocket(acquired.connection, "aborted")
		})
		defer stopCancel()
		message := first
		// startCodexWebSocketAttempt observed the first frame.
		observed := true
		pulled := false
		for {
			if !pulled {
				select {
				case <-pull:
					pulled = true
				case <-parseDone:
					return
				}
			}
			if !observed {
				if err := builder.observeCodexFrame(message); err != nil {
					_ = writer.CloseWithError(err)
					return
				}
			}
			observed = false
			mapped, mapErr := mapCodexWebSocketEventFrame(message)
			if mapErr != nil {
				_ = writer.CloseWithError(mapErr)
				return
			}
			if !mapped.skip {
				if mapped.terminal {
					markCodexWebSocketFinishing(acquired.entry)
				}
				if _, err := fmt.Fprintf(writer, "data: %s\n\n", mapped.data); err != nil {
					return
				}
				pulled = false
				if mapped.terminal {
					return
				}
			}
			messageType, next, err := readCodexWebSocket(ctx, acquired.connection, optionTimeout(opts.TimeoutMs))
			if err != nil {
				if ctx.Err() == nil {
					recordCodexWebSocketFailure(acquired.sessionID, err)
					err = &codexWebSocketTransportFailure{err: err}
				}
				_ = writer.CloseWithError(err)
				return
			}
			if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
				_ = writer.CloseWithError(fmt.Errorf("unexpected WebSocket message type %d", messageType))
				return
			}
			message = next
		}
	}()

	decoder := newSSEDecoder(&codexTransportFailureReader{Reader: reader, ctx: ctx, builder: builder, transport: opts.Transport, requestBytes: requestBytes})
	decoder.beforeNext = func() {
		builder.publishPending()
		select {
		case pull <- struct{}{}:
		case <-feedDone:
		}
	}
	p.parseResponses(ctx, decoder, builder, grammarProps)
	close(parseDone)
	_ = reader.Close()
	<-feedDone
	result := builder.stream.Result()
	keep := result != nil && result.StopReason != StopReasonError && result.StopReason != StopReasonAborted
	if keep && useCachedContext && acquired.entry != nil && result.ResponseID != "" {
		responseItems, err := p.convertAnchoredMessages([]Message{*result}, grammarProps, false)
		baseline := make([]json.RawMessage, len(request.input), len(request.input)+len(responseItems))
		copy(baseline, request.input)
		// Each response item as the next request's body writes it: JSON.stringify text, compared byte for byte.
		for _, item := range responseItems {
			if err != nil {
				break
			}
			var encoded []byte
			encoded, err = marshalRequestBody(item)
			baseline = append(baseline, encoded)
		}
		if err == nil {
			codexWebSocketSessions.mu.Lock()
			acquired.entry.continuation = &codexWebSocketContinuation{
				lastRest:       request.rest(),
				lastResponseID: result.ResponseID,
				baseline:       baseline,
			}
			codexWebSocketSessions.mu.Unlock()
		}
	}
	acquired.release(keep)
}

func codexWebSocketEventErrorCode(message []byte) string {
	var event struct {
		Type  string `json:"type"`
		Code  string `json:"code"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
		Response *struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(message, &event) != nil {
		return ""
	}
	if event.Code != "" {
		return event.Code
	}
	if event.Error != nil && event.Error.Code != "" {
		return event.Error.Code
	}
	if event.Response != nil && event.Response.Error != nil {
		return event.Response.Error.Code
	}
	return ""
}
