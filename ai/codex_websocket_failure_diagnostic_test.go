package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// codexWebSocketFailureRun serves one Codex request: the WebSocket upgrade runs ws (nil answers 404), and a POST answers the SSE fallback. It returns the final message and the length of the request body the provider sent.
func codexWebSocketFailureRun(t *testing.T, ws func(*websocket.Conn)) (*AssistantMessage, int) {
	t.Helper()
	return codexWebSocketFailureRunWith(t, ws, StreamOptions{})
}

// codexWebSocketFailureRunWith is codexWebSocketFailureRun with the caller's stream options.
func codexWebSocketFailureRunWith(t *testing.T, ws func(*websocket.Conn), opts StreamOptions) (*AssistantMessage, int) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	bodyBytes := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if ws == nil {
				http.NotFound(w, r)
				return
			}
			connection, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = connection.Close() }()
			_, request, err := connection.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var create map[string]any
			_ = json.Unmarshal(request, &create)
			delete(create, "type")
			encoded, _ := orderedjson.Marshal(create)
			select {
			case bodyBytes <- len(encoded):
			default:
			}
			ws(connection)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_sse\",\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey: codexTestToken(t, "acct_failure"), Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL,
	})
	// A frame the client wrongly skips leaves it waiting on a server that holds the connection; the test budget turns that into an aborted result.
	stream, err := provider.Stream(testbudget.Context(t), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), opts)
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	select {
	case n := <-bodyBytes:
		return result, n
	default:
		return result, -1
	}
}

func codexDiagnosticJSON(t *testing.T, message *AssistantMessage) string {
	t.Helper()
	if len(message.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want one", message.Diagnostics)
	}
	encoded, err := json.Marshal(message.Diagnostics[0])
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	return `{"error":` + string(object["error"]) + `,"details":` + string(object["details"]) + `}`
}

// openai-codex-responses.ts:340-372: a WebSocket transport failure appends a provider_transport_failure diagnostic with the Node WebSocket error (extractWebSocketError, extractWebSocketCloseError) and the literal's details. Before the message stream starts the request falls back to SSE; after it the error ends the message with that text, which retry.ts's "websocket.?error"/"websocket.?closed" patterns retry. Probed against Pi 1.1.0 (probe wscodex.sh).
func TestCodexWebSocketTransportFailureIsPisDiagnosticAndErrorText(t *testing.T) {
	created := `{"type":"response.created","response":{"id":"r","status":"in_progress"}}`
	delta := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"m","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"Hi"}`,
	}
	send := func(connection *websocket.Conn, frames ...string) {
		for _, frame := range frames {
			if err := connection.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				t.Error(err)
			}
		}
	}
	before := func(errorJSON string) string {
		return `{"error":` + errorJSON + `,"details":{"configuredTransport":"auto","fallbackTransport":"sse","eventsEmitted":false,"phase":"before_message_stream_start","requestBytes":%d}}`
	}
	after := func(errorJSON string) string {
		return `{"error":` + errorJSON + `,"details":{"configuredTransport":"auto","eventsEmitted":true,"phase":"after_message_stream_start","requestBytes":%d}}`
	}
	for _, test := range []struct {
		name        string
		ws          func(*websocket.Conn)
		wantStop    StopReason
		wantError   string
		wantDetails string
	}{
		{"handshake refused", nil, StopReasonStop, "", before(`{"name":"Error","message":"WebSocket error"}`)},
		{"dropped before the first event", func(*websocket.Conn) {}, StopReasonStop, "", before(`{"name":"Error","message":"WebSocket error"}`)},
		{"closed before the first event", func(connection *websocket.Conn) {
			_ = connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1011, "boom"))
		}, StopReasonStop, "", before(`{"name":"WebSocketCloseError","message":"WebSocket closed 1011 boom","code":1011}`)},
		{"dropped after the stream started", func(connection *websocket.Conn) {
			send(connection, append([]string{created}, delta...)...)
		}, StopReasonError, "WebSocket error", after(`{"name":"Error","message":"WebSocket error"}`)},
		{"closed after the stream started", func(connection *websocket.Conn) {
			send(connection, created)
			_ = connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1000, ""))
		}, StopReasonError, "WebSocket closed 1000", after(`{"name":"WebSocketCloseError","message":"WebSocket closed 1000","code":1000}`)},
		// openai-codex-responses.ts:344-353: the retry also hits the limit, so connectionLimitBeforeStart keeps the CodexApiError on the fallback path. Pi 1.1.0 (probe wscodex.sh frames-ok with limit.txt) gives this diagnostic.
		{"connection limit after its retry", func(connection *websocket.Conn) {
			send(connection, `{"type":"error","code":"websocket_connection_limit_reached","message":"Too many connections"}`)
			for {
				if _, _, err := connection.ReadMessage(); err != nil {
					return
				}
			}
		}, StopReasonStop, "", before(`{"name":"CodexApiError","message":"Codex error: Too many connections","code":"websocket_connection_limit_reached"}`)},
		{"closed too big after the stream started", func(connection *websocket.Conn) {
			send(connection, created)
			_ = connection.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(1009, ""))
		}, StopReasonError, "WebSocket closed 1009 message too big", after(`{"name":"WebSocketCloseError","message":"WebSocket closed 1009 message too big","code":1009}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, requestBytes := codexWebSocketFailureRun(t, test.ws)
			if result.StopReason != test.wantStop || result.ErrorMessage != test.wantError {
				t.Fatalf("result = %s %q, want %s %q", result.StopReason, result.ErrorMessage, test.wantStop, test.wantError)
			}
			if test.ws == nil {
				requestBytes = -1
			}
			got := codexDiagnosticJSON(t, result)
			if requestBytes < 0 {
				// The handshake failed, so the request body never crossed the WebSocket; compare everything else.
				got = got[:strings.LastIndex(got, `"requestBytes":`)]
				want := strings.Replace(test.wantDetails, `"requestBytes":%d}}`, "", 1)
				if got != want {
					t.Errorf("diagnostic = %s, want %s", got, want)
				}
				return
			}
			if want := strings.Replace(test.wantDetails, "%d", itoaForTest(requestBytes), 1); got != want {
				t.Errorf("diagnostic = %s, want %s", got, want)
			}
		})
	}
}

func itoaForTest(n int) string {
	encoded, _ := json.Marshal(n)
	return string(encoded)
}

// openai-codex-responses.ts:341-354: an abort rethrows before the transport-failure branch, so an aborted WebSocket stream carries no provider_transport_failure diagnostic, although its connection fails as a dropped one does.
func TestCodexWebSocketAbortAfterTheStreamStartedAddsNoTransportDiagnostic(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "the aborted request must not fall back to SSE", http.StatusTeapot)
			return
		}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = connection.Close() }()
		if _, _, err := connection.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		for _, frame := range []string{
			`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[]}}`,
			`{"type":"response.content_part.added","item_id":"m","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
			`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"Hi"}`,
		} {
			if err := connection.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				t.Error(err)
				return
			}
		}
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	defer CloseOpenAICodexWebSocketSessions()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey: codexTestToken(t, "acct_abort"), Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL,
	})
	ctx, cancel := context.WithCancel(testbudget.Context(t))
	defer cancel()
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for event := range stream.Events(context.WithoutCancel(ctx)) {
		if event.EventType() == "text_delta" {
			cancel()
		}
	}
	result := stream.Result()
	if result.StopReason != StopReasonAborted {
		t.Fatalf("result = %s %q, want aborted", result.StopReason, result.ErrorMessage)
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("diagnostics = %+v, want none: an aborted request is not a transport failure", result.Diagnostics)
	}
}

// openai-codex-responses.ts:1325-1348 parseWebSocket hands each non-empty text frame to JSON.parse as it is and reads `parsed.type`: "[DONE]" and other invalid text fail with JSON.parse's message, null fails reading `type`, and any other value without a string type is skipped by mapCodexEvents. A valid frame may be written over several lines. Probed against Pi 1.1.0 (probe wscodex.sh with FRAMES).
// mapCodexEvents (:745-755) hands every queued value to onProviderStreamEvent, a value without a type included, but a frame that fails in parseWebSocket is never queued, so the observer never sees "[DONE]", other invalid text or null. Pi 1.1.0 with a provider_stream_event extension observed 3 events before a null frame and the 5 between events.
func TestCodexWebSocketFramesAreParsedLikePi(t *testing.T) {
	created := `{"type":"response.created","response":{"id":"r","status":"in_progress"}}`
	events := []string{
		created,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"m","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`,
	}
	indented := make([]string, len(events))
	for i, event := range events {
		var buffer bytes.Buffer
		if err := json.Indent(&buffer, []byte(event), "", "\t"); err != nil {
			t.Fatal(err)
		}
		indented[i] = "\r\n" + buffer.String() + " \n"
	}
	for _, test := range []struct {
		name         string
		frames       []string
		wantStop     StopReason
		wantError    string
		wantObserved []string
	}{
		{"[DONE] is invalid JSON", append(slices.Clone(events[:4]), "[DONE]"), StopReasonError, `Invalid Codex WebSocket JSON: Unexpected token 'D', "[DONE]" is not valid JSON`, events[:4]},
		{"null has no type", append(slices.Clone(events[:4]), "null"), StopReasonError, "Invalid Codex WebSocket JSON: Cannot read properties of null (reading 'type')", events[:4]},
		{"text that is not JSON", append(slices.Clone(events[:4]), "not json"), StopReasonError, `Invalid Codex WebSocket JSON: Unexpected token 'o', "not json" is not valid JSON`, events[:4]},
		{"values without a type are skipped", append(append(slices.Clone(events[:4]), "5", "[1]", `"x"`, `{"a":1}`), events[4:]...), StopReasonStop, "", append(append(slices.Clone(events[:4]), "5", "[1]", `"x"`, `{"a":1}`), events[4:]...)},
		{"frames written over several lines", indented, StopReasonStop, "", events},
	} {
		t.Run(test.name, func(t *testing.T) {
			var observed []string
			observe := func(_ context.Context, data any, _ *Model) error {
				encoded, err := json.Marshal(data)
				if err != nil {
					t.Error(err)
				}
				observed = append(observed, string(encoded))
				return nil
			}
			result, _ := codexWebSocketFailureRunWith(t, func(connection *websocket.Conn) {
				for _, frame := range test.frames {
					if err := connection.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
						t.Error(err)
					}
				}
				// Hold the connection until the client is done, as a server that stops sending would.
				for {
					if _, _, err := connection.ReadMessage(); err != nil {
						return
					}
				}
			}, StreamOptions{OnProviderStreamEvent: observe})
			if result.StopReason != test.wantStop || result.ErrorMessage != test.wantError {
				t.Fatalf("result = %s %q, want %s %q", result.StopReason, result.ErrorMessage, test.wantStop, test.wantError)
			}
			// The observer's data is a Go value, so compare it with each frame decoded the same way.
			var want []string
			for _, frame := range test.wantObserved {
				var value any
				if err := json.Unmarshal([]byte(frame), &value); err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(value)
				want = append(want, string(encoded))
			}
			if !slices.Equal(observed, want) {
				t.Errorf("observed = %q, want %q", observed, want)
			}
			if test.wantStop == StopReasonStop && (len(result.Content) != 1 || result.Content[0].(TextContent).Text != "Hi") {
				t.Errorf("content = %+v, want Hi", result.Content)
			}
			if len(result.Diagnostics) != 0 {
				t.Errorf("diagnostics = %+v, want none: a protocol error is not a transport failure", result.Diagnostics)
			}
		})
	}
}
