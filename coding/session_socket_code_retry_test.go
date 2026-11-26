package coding

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 4699b5b269 (#12136, #12128): the session's retry path for the transport events whose Node codes retry.ts now lists, with the event
// produced for real by a hermetic server and each provider shape of closedConnectionAPIs (an API key, an OAuth token, an
// OpenAI-compatible gateway with a custom base URL over openai-completions and over openai-responses). The fetch-based APIs report the
// event behind their SDK's wrapper text ("Connection error.: read ECONNRESET"), which Pi and PiG retried before the commit: these tests
// pin that the path stays one behaviour. The bare code, which the commit adds, is Bedrock's (ai/providers/bedrock
// TestBedrockBareSocketCodeIsRetriedByTheAssistantRetryLoop). The mid-body close is TestSessionRetriesAServerSocketClosedBeforeTheAnswer.

// resetConnection resets the request's connection (RST, not FIN) without a response.
func resetConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Error("test server does not support hijacking")
		return
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = connection.Close()
}

// A listener that resets the connection after the request headers (ECONNRESET): one failed request, one retry, the answer arrives.
func TestSessionRetriesAConnectionResetAfterTheRequestHeaders(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					resetConnection(t, w)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(api.okBody))
			}))
			t.Cleanup(server.Close)
			h := closedConnectionHarness(t, api, server, retryEventsSettings)
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := requests.Load(); got != 2 {
				t.Fatalf("server requests = %d, want 2 (the reset request and one retry)", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
				t.Fatalf("retry events = %v", got)
			}
			if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != "Connection error.: read ECONNRESET" {
				t.Fatalf("auto_retry_start = %#v, want the reset reported as Connection error.: read ECONNRESET", starts)
			}
			if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
				t.Fatalf("last assistant text = %v", text)
			}
		})
	}
}

// A listener that always resets: the request and the configured retries, then the error is surfaced.
func TestSessionSurfacesAConnectionResetAfterTheConfiguredRetries(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				resetConnection(t, w)
			}))
			t.Cleanup(server.Close)
			h := closedConnectionHarness(t, api, server, retrySettings(2, 60000))
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := requests.Load(); got != 3 {
				t.Fatalf("server requests = %d, want 3 (the request and two retries)", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "start:2", "end:false"}) {
				t.Fatalf("retry events = %v", got)
			}
			messages := h.session.Messages()
			last := messages[len(messages)-1].Assistant
			if last == nil || last.StopReason != ai.StopReasonError || last.ErrorMessage != "Connection error.: read ECONNRESET" {
				t.Fatalf("last message = %#v, want the surfaced reset", last)
			}
		})
	}
}

// A listener that does not answer until the request's own deadline (retry.provider.timeoutMs): the timeout class. The handler reads the request
// and then waits for the client to give it up, so the deadline, not the test, decides the event. Reading the body first is what lets it wait:
// net/http's server learns of a peer's close from its background read, which starts only when the request body has been read to its end, so a
// handler that never read the body is never told, and server.Close then waits for it without end. The client does close the connection at
// the deadline (TestSessionProviderTimeoutClosesTheRequestConnection).
func TestSessionRetriesARequestThatReachesItsDeadline(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						return
					}
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(api.okBody))
			}))
			t.Cleanup(server.Close)
			h := closedConnectionHarness(t, api, server, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1,"provider":{"timeoutMs":1000,"maxRetries":0}}}`)
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := requests.Load(); got != 2 {
				t.Fatalf("server requests = %d, want 2 (the request that reached its deadline and one retry)", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
				t.Fatalf("retry events = %v", got)
			}
			starts := eventsOf[agent.AutoRetryStartEvent](log)
			if len(starts) != 1 || !strings.Contains(strings.ToLower(starts[0].ErrorMessage), "timed out") {
				t.Fatalf("auto_retry_start = %#v, want the deadline reported as a timeout", starts)
			}
			t.Logf("deadline error message: %q", starts[0].ErrorMessage)
			if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
				t.Fatalf("last assistant text = %v", text)
			}
		})
	}
}

// What is not retried: a 400 whose message names a socket code inside a longer identifier (retry.test.ts "Invalid value EXECONNRESET").
// One request, no retry event, the provider's error is the result.
func TestSessionDoesNotRetryABadRequestThatNamesASocketCodeInsideAnIdentifier(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				if strings.HasPrefix(api.name, "anthropic") {
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"Invalid value EXECONNRESET"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"error":{"message":"Invalid value EXECONNRESET","type":"invalid_request_error"}}`))
			}))
			t.Cleanup(server.Close)
			h := closedConnectionHarness(t, api, server, retryEventsSettings)
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := requests.Load(); got != 1 {
				t.Fatalf("server requests = %d, want 1: the bad request is not retried", got)
			}
			if got := retrySummary(log); len(got) != 0 {
				t.Fatalf("retry events = %v, want none", got)
			}
			messages := h.session.Messages()
			last := messages[len(messages)-1].Assistant
			if last == nil || last.StopReason != ai.StopReasonError || !strings.Contains(last.ErrorMessage, "Invalid value EXECONNRESET") || ai.IsRetryableAssistantError(ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: last.ErrorMessage}) {
				t.Fatalf("last message = %#v, want the surfaced bad request, not retryable", last)
			}
		})
	}
}

// bareSocketCodeAPI is api with the gateway's error event reading only a Node socket error code, the form retry.ts lists since Pi 4699b5b26.
func bareSocketCodeAPI(api closedConnectionAPI, text string) closedConnectionAPI {
	api.dropBody = strings.ReplaceAll(api.dropBody, "Connection closed.", text)
	api.wantError = strings.ReplaceAll(api.wantError, "Connection closed.", text)
	return api
}

// The bare Node form, relayed as text by the provider's stream (an error event that copies the upstream socket error): one failed
// request, one retry, the answer arrives. Before Pi 4699b5b26 no pattern matched the text and the turn ended with it.
func TestSessionRetriesABareSocketCodeOnce(t *testing.T) {
	for _, text := range []string{"read ECONNRESET", "write EPIPE", "connect ETIMEDOUT 140.82.113.3:443"} {
		for _, api := range closedConnectionAPIs() {
			api = bareSocketCodeAPI(api, text)
			t.Run(text+"/"+api.name, func(t *testing.T) {
				server, requests := closedConnectionServer(t, api, 1)
				h := closedConnectionHarness(t, api, server, retryEventsSettings)
				log := recordSessionEvents(h.session)
				retryPrompt(t, h, "hello")
				if got := requests.Load(); got != 2 {
					t.Fatalf("server requests = %d, want 2 (one failure, one retry)", got)
				}
				if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
					t.Fatalf("retry events = %v", got)
				}
				if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != api.wantError || starts[0].MaxAttempts != 3 {
					t.Fatalf("auto_retry_start = %#v, want error %q with 3 attempts", starts, api.wantError)
				}
				if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
					t.Fatalf("last assistant text = %v", text)
				}
			})
		}
	}
}

// A provider that always reports the bare code: the request and the configured retries, then the error is surfaced.
func TestSessionSurfacesABareSocketCodeAfterTheConfiguredRetries(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		api = bareSocketCodeAPI(api, "read ECONNRESET")
		t.Run(api.name, func(t *testing.T) {
			server, requests := closedConnectionServer(t, api, 1<<30)
			h := closedConnectionHarness(t, api, server, retrySettings(2, 60000))
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := requests.Load(); got != 3 {
				t.Fatalf("server requests = %d, want 3 (the request and two retries)", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "start:2", "end:false"}) {
				t.Fatalf("retry events = %v", got)
			}
			if ends := eventsOf[agent.AutoRetryEndEvent](log); len(ends) != 1 || ends[0].Success || ends[0].Attempt != 2 || ends[0].FinalError != api.wantError {
				t.Fatalf("auto_retry_end = %#v, want failure after attempt 2 with %q", ends, api.wantError)
			}
		})
	}
}

// The same event with the code inside a longer identifier is the provider's own error: one request, no retry.
func TestSessionDoesNotRetryASocketCodeInsideAnIdentifier(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		api = bareSocketCodeAPI(api, "Invalid value EXECONNRESET")
		api.dropBody = strings.ReplaceAll(api.dropBody, `"type":"gateway_notice"`, `"type":"invalid_request_error"`)
		api.wantError = strings.ReplaceAll(api.wantError, `"type":"gateway_notice"`, `"type":"invalid_request_error"`)
		t.Run(api.name, func(t *testing.T) {
			server, requests := closedConnectionServer(t, api, 1<<30)
			h := closedConnectionHarness(t, api, server, retryEventsSettings)
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := requests.Load(); got != 1 {
				t.Fatalf("server requests = %d, want 1", got)
			}
			if got := retrySummary(log); len(got) != 0 {
				t.Fatalf("retry events = %v, want none", got)
			}
		})
	}
}
