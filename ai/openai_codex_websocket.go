package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	codexWebSocketBeta           = "responses_websockets=2026-02-06"
	codexWebSocketConnectTimeout = 15 * time.Second
	codexWebSocketCacheTTL       = 5 * time.Minute
	codexWebSocketMaxAge         = 55 * time.Minute
)

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

type codexWebSocketContinuation struct {
	lastRequestBody   map[string]any
	lastResponseID    string
	lastResponseItems []any
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

func resolveCodexWebSocketURL(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("openai-codex-responses: unsupported WebSocket URL scheme %q", parsed.Scheme)
	}
	return parsed.String(), nil
}

func codexWebSocketHeaders(base map[string]string, options ProviderHeaders, token, accountID, requestID string) http.Header {
	headers := make(http.Header, len(base)+5)
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
	headers.Set("originator", "pi")
	// pig divergence (D65): Codex applies PiG's product identity after model
	// and request headers, preserving upstream precedence.
	headers.Set("User-Agent", PiUserAgent())
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
	if connectTimeout <= 0 {
		connectTimeout = codexWebSocketConnectTimeout
	}
	var connectionMu sync.Mutex
	var dialingConnection net.Conn
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: connectTimeout}
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
		return nil, err
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
	return messageType, data, err
}

func closeCodexWebSocket(connection *websocket.Conn, reason string) {
	if connection == nil {
		return
	}
	deadline := time.Now().Add(time.Second)
	_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason), deadline)
	_ = connection.Close()
}

func cloneCodexRequestBody(body map[string]any) map[string]any {
	encoded, _ := json.Marshal(body)
	var clone map[string]any
	_ = json.Unmarshal(encoded, &clone)
	return clone
}

func codexRequestWithoutInput(body map[string]any) map[string]any {
	clone := cloneCodexRequestBody(body)
	delete(clone, "input")
	delete(clone, "previous_response_id")
	return clone
}

func codexInputItems(body map[string]any) []any {
	items, _ := body["input"].([]any)
	return items
}

func buildCachedCodexRequestBody(entry *codexWebSocketEntry, body map[string]any) map[string]any {
	request := cloneCodexRequestBody(body)
	if entry == nil || entry.continuation == nil {
		return request
	}
	continuation := entry.continuation
	if !reflect.DeepEqual(codexRequestWithoutInput(body), codexRequestWithoutInput(continuation.lastRequestBody)) {
		entry.continuation = nil
		return request
	}
	baseline := append([]any{}, codexInputItems(continuation.lastRequestBody)...)
	baseline = append(baseline, continuation.lastResponseItems...)
	current := codexInputItems(body)
	if len(current) < len(baseline) || !reflect.DeepEqual(current[:len(baseline)], baseline) {
		entry.continuation = nil
		return request
	}
	request["previous_response_id"] = continuation.lastResponseID
	request["input"] = append([]any(nil), current[len(baseline):]...)
	return request
}

func (p *openAIResponsesProvider) startCodexWebSocket(ctx context.Context, endpoint string, body []byte, apiKey, accountID, cacheSessionID string, grammarProps map[string]string, opts StreamOptions) (*AssistantMessageEventStream, error) {
	return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, true, true)
}

func (p *openAIResponsesProvider) startCodexWebSocketAttempt(ctx context.Context, endpoint string, body []byte, apiKey, accountID, cacheSessionID string, grammarProps map[string]string, opts StreamOptions, retryConnectionLimit, retryMissingContinuation bool) (*AssistantMessageEventStream, error) {
	if codexWebSocketFallbackActive(cacheSessionID) {
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil
	}
	var fullBody map[string]any
	if err := json.Unmarshal(body, &fullBody); err != nil {
		return nil, fmt.Errorf("openai-codex-responses: WebSocket payload: %w", err)
	}
	requestID := cacheSessionID
	if requestID == "" {
		requestID = uuid.Must(uuid.NewV7()).String()
	}
	headers := codexWebSocketHeaders(p.cfg.ExtraHeaders, opts.Headers, apiKey, accountID, requestID)
	connectTimeout := time.Duration(opts.WebSocketConnectTimeoutMs) * time.Millisecond
	acquired, err := acquireCodexWebSocket(ctx, endpoint, headers, cacheSessionID, accountID, connectTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil
	}
	// Empty transport has upstream's default "auto" behavior, including cached
	// continuation when a cache session id is available.
	useCachedContext := opts.Transport == TransportWebSocketCached || opts.Transport == TransportAuto || opts.Transport == ""
	requestBody := cloneCodexRequestBody(fullBody)
	if useCachedContext {
		requestBody = buildCachedCodexRequestBody(acquired.entry, fullBody)
	}
	requestBody["type"] = "response.create"
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		acquired.release(false)
		return nil, err
	}
	if err := acquired.connection.WriteMessage(websocket.TextMessage, encoded); err != nil {
		wasReused := acquired.reused
		acquired.release(false)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if wasReused {
			return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, retryConnectionLimit, retryMissingContinuation)
		}
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil
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
		if requestBody["store"] == true {
			stats.StoreTrueRequests++
		}
		stats.LastInputItems = len(codexInputItems(requestBody))
		if previous, _ := requestBody["previous_response_id"].(string); previous != "" {
			stats.DeltaRequests++
			count := len(codexInputItems(requestBody))
			stats.LastDeltaInputItems = &count
			stats.LastPreviousResponseId = previous
		} else {
			stats.FullContextRequests++
			stats.LastDeltaInputItems = nil
			stats.LastPreviousResponseId = ""
		}
		codexWebSocketSessions.mu.Unlock()
	}

	messageType, first, err := readCodexWebSocket(ctx, acquired.connection, time.Duration(opts.TimeoutMs)*time.Millisecond)
	if err != nil {
		wasReused := acquired.reused
		acquired.release(false)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if wasReused {
			return p.startCodexWebSocketAttempt(ctx, endpoint, body, apiKey, accountID, cacheSessionID, grammarProps, opts, retryConnectionLimit, retryMissingContinuation)
		}
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil
	}
	if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
		acquired.release(false)
		err := fmt.Errorf("openai-codex-responses: unexpected WebSocket message type %d", messageType)
		recordCodexWebSocketFailure(cacheSessionID, err)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil
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
		_, eventErr := mapCodexWebSocketEventFrame(first)
		if eventErr == nil {
			eventErr = errors.New("Codex error: websocket_connection_limit_reached")
		}
		recordCodexWebSocketFailure(cacheSessionID, eventErr)
		recordCodexWebSocketSSEFallback(cacheSessionID)
		return nil, nil
	}

	builder := newAssistantStreamBuilder(ctx, APIOpenAICodexResponses, p.cfg.ProviderID, p.cfg.Model)
	builder.modelCost = opts.ModelCost
	go p.consumeCodexWebSocket(ctx, acquired, first, fullBody, grammarProps, useCachedContext, builder, opts)
	return builder.stream, nil
}

func markCodexWebSocketFinishing(entry *codexWebSocketEntry) {
	if entry == nil {
		return
	}
	codexWebSocketSessions.mu.Lock()
	entry.finishing = true
	codexWebSocketSessions.mu.Unlock()
}

func (p *openAIResponsesProvider) consumeCodexWebSocket(ctx context.Context, acquired *acquiredCodexWebSocket, first []byte, fullBody map[string]any, grammarProps map[string]string, useCachedContext bool, builder *assistantStreamBuilder, opts StreamOptions) {
	reader, writer := io.Pipe()
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		defer func() { _ = writer.Close() }()
		stopCancel := context.AfterFunc(ctx, func() {
			closeCodexWebSocket(acquired.connection, "aborted")
		})
		defer stopCancel()
		message := first
		for {
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
				if mapped.terminal {
					return
				}
			}
			messageType, next, err := readCodexWebSocket(ctx, acquired.connection, time.Duration(opts.TimeoutMs)*time.Millisecond)
			if err != nil {
				if ctx.Err() == nil {
					recordCodexWebSocketFailure(acquired.sessionID, err)
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

	p.parseResponsesSSE(ctx, reader, builder, grammarProps)
	_ = reader.Close()
	<-feedDone
	result := builder.stream.Result()
	keep := result != nil && result.StopReason != StopReasonError && result.StopReason != StopReasonAborted
	if keep && useCachedContext && acquired.entry != nil && result.ResponseID != "" {
		responseItems, err := p.convertAnchoredMessages([]Message{*result}, grammarProps, false)
		if err == nil {
			encoded, _ := json.Marshal(responseItems)
			var items []any
			_ = json.Unmarshal(encoded, &items)
			codexWebSocketSessions.mu.Lock()
			acquired.entry.continuation = &codexWebSocketContinuation{
				lastRequestBody:   cloneCodexRequestBody(fullBody),
				lastResponseID:    result.ResponseID,
				lastResponseItems: items,
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
