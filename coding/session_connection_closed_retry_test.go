package coding

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi 9b88b94e2 (#12093, #12064): a gateway that ends a stream with an error event reading "Connection closed." is retried.
//
// Retry contract, as Pi's AgentSession has it and PiG's Session keeps it: Prompt (the caller) waits for the whole retry cycle; the
// failed attempt's error message is classified (ai.IsRetryableAssistantError) after the provider returns it as the assistant message,
// never as a Go error; a retry waits baseDelayMs * 2^(attempt-1) (capped) with auto_retry_start published before the wait and the next
// request sent after it; AbortRetry during the wait cancels it (auto_retry_end, success false, "Retry cancelled", no further request);
// after maxRetries retries the last error is surfaced as the assistant message and auto_retry_end reports success false.

// closedConnectionAPI is one API kind (and credential shape) the retry classification reaches, with the hermetic server's two bodies:
// the gateway's error event, and a complete answer.
type closedConnectionAPI struct {
	name string
	// provider builds the real provider against the hermetic server.
	provider func(baseURL string) ai.Provider
	// dropBody is the stream a gateway ends with "Connection closed.", in the wording of this API kind's stream.
	dropBody string
	// wantError is the error message the provider reports for dropBody.
	wantError string
	okBody    string
	// custom is a provider id the registry does not know (an OpenAI-compatible gateway): the session needs it registered to find its key.
	custom bool
}

const (
	closedAnthropicEvent = `{"type":"error","error":{"type":"api_error","message":"Connection closed."}}`

	closedAnthropicOK = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered after retry\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	closedCompletionsOK = "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"recovered after retry\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	closedResponsesOK = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n" +
		"data: {\"type\":\"response.content_part.added\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"recovered after retry\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered after retry\",\"annotations\":[]}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":3,\"total_tokens\":4}}}\n\n"
)

func closedConnectionAPIs() []closedConnectionAPI {
	return []closedConnectionAPI{
		{
			// The case of the issue: Anthropic-style api_error event; the Anthropic SDK's error message is the event's JSON.
			name: "anthropic API key",
			provider: func(u string) ai.Provider {
				return ai.NewAnthropicProvider(ai.AnthropicConfig{BaseURL: u, APIKey: "test-key", Model: "claude-sonnet-4-20250514"})
			},
			dropBody:  "event: error\ndata: " + closedAnthropicEvent + "\n\n",
			wantError: closedAnthropicEvent,
			okBody:    closedAnthropicOK,
		},
		{
			name: "anthropic OAuth token",
			provider: func(u string) ai.Provider {
				return ai.NewAnthropicProvider(ai.AnthropicConfig{BaseURL: u, APIKey: "sk-ant-oat-test-token", Model: "claude-sonnet-4-20250514"})
			},
			dropBody:  "event: error\ndata: " + closedAnthropicEvent + "\n\n",
			wantError: closedAnthropicEvent,
			okBody:    closedAnthropicOK,
		},
		{
			// An OpenAI-compatible gateway with a custom base URL relays the upstream failure as an error chunk.
			name: "openai-completions custom base URL",
			provider: func(u string) ai.Provider {
				return ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: u, APIKey: "test-key", Model: "gateway-model", ProviderID: "my-gateway"})
			},
			dropBody:  "data: {\"error\":{\"message\":\"Connection closed.\",\"type\":\"api_error\"}}\n\n",
			wantError: "Connection closed.",
			okBody:    closedCompletionsOK,
			custom:    true,
		},
		{
			name: "openai-responses API key",
			provider: func(u string) ai.Provider {
				return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: u, APIKey: "test-key", Model: "gpt-test", ProviderID: "openai"})
			},
			dropBody:  "data: {\"type\":\"error\",\"code\":\"api_error\",\"message\":\"Connection closed.\"}\n\n",
			wantError: "Error Code api_error: Connection closed.",
			okBody:    closedResponsesOK,
		},
		{
			name: "openai-responses custom base URL",
			provider: func(u string) ai.Provider {
				return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: u, APIKey: "test-key", Model: "gateway-model", ProviderID: "my-gateway"})
			},
			dropBody:  "data: {\"type\":\"error\",\"code\":\"api_error\",\"message\":\"Connection closed.\"}\n\n",
			wantError: "Error Code api_error: Connection closed.",
			okBody:    closedResponsesOK,
			custom:    true,
		},
	}
}

// closedConnectionServer answers the first `failures` requests with the gateway's error event and every later request with a complete
// stream, and counts the requests it received.
func closedConnectionServer(t *testing.T, api closedConnectionAPI, failures int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1))
		w.Header().Set("Content-Type", "text/event-stream")
		body := api.okBody
		if n <= failures {
			body = api.dropBody
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func closedConnectionHarness(t *testing.T, api closedConnectionAPI, server *httptest.Server, settings string) *recoveryHarness {
	t.Helper()
	provider := api.provider(server.URL)
	h := newBoundaryHarness(t, harnessOptions{settings: settings, provider: provider})
	// The session resolves the request key by the provider's id, as it does for any provider; the provider itself sends its own configured key.
	if err := h.session.services.Auth().Set(provider.ID(), ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}); err != nil {
		t.Fatal(err)
	}
	if api.custom {
		// A registered model under an id no scripted model uses, as configureFauxAuth does: registration marks the provider configured in the availability snapshot that prompt validation reads.
		if err := h.session.services.Registry().RegisterExtensionProvider(provider.ID(), extension.ProviderConfig{API: "openai-completions", APIKey: "test-key", BaseURL: "https://gateway.invalid", Models: []extension.ProviderModelConfig{
			{ID: "gateway-auth", Name: "gateway-auth", ContextWindow: 128_000, MaxTokens: 4096, Input: []string{"text"}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// One failed request, one retry, the answer arrives.
func TestSessionRetriesAGatewayClosedConnectionOnce(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
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
			starts := eventsOf[agent.AutoRetryStartEvent](log)
			if len(starts) != 1 || starts[0].ErrorMessage != api.wantError || starts[0].MaxAttempts != 3 {
				t.Fatalf("auto_retry_start = %#v, want error %q with 3 attempts", starts, api.wantError)
			}
			text := h.session.LastAssistantText()
			if text == nil || *text != "recovered after retry" {
				t.Fatalf("last assistant text = %v, want the answer of the retried request", text)
			}
			if sessionIsRetrying(h.session) {
				t.Fatal("session still retrying after the answer arrived")
			}
		})
	}
}

// A connection that is always closed: the configured number of retries, then the error is surfaced.
func TestSessionSurfacesAGatewayClosedConnectionAfterTheConfiguredRetries(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
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
			ends := eventsOf[agent.AutoRetryEndEvent](log)
			if len(ends) != 1 || ends[0].Success || ends[0].Attempt != 2 || ends[0].FinalError != api.wantError {
				t.Fatalf("auto_retry_end = %#v, want failure after attempt 2 with %q", ends, api.wantError)
			}
			messages := h.session.Messages()
			last := messages[len(messages)-1].Assistant
			if last == nil || last.StopReason != ai.StopReasonError || last.ErrorMessage != api.wantError {
				t.Fatalf("last message = %#v, want the surfaced provider error %q", last, api.wantError)
			}
			if sessionIsRetrying(h.session) {
				t.Fatal("session still retrying after the last attempt")
			}
		})
	}
}

// AbortRetry during the backoff cancels the wait: no second request, the cycle ends with "Retry cancelled".
func TestSessionAbortRetryDuringAClosedConnectionBackoff(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			server, requests := closedConnectionServer(t, api, 1<<30)
			h := closedConnectionHarness(t, api, server, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":60000,"maxAgentDelayMs":60000}}`)
			log := recordSessionEvents(h.session)
			sawRetryStart := make(chan struct{})
			var once sync.Once
			h.session.Subscribe(func(event agent.AgentEvent) {
				if _, ok := event.(agent.AutoRetryStartEvent); ok {
					once.Do(func() { close(sawRetryStart) })
				}
			})
			done := make(chan error, 1)
			go func() { done <- h.session.Prompt(t.Context(), "hello", nil) }()
			select {
			case <-sawRetryStart:
			case <-time.After(testbudget.Wait(t)):
				t.Fatal("auto_retry_start was not published for the closed connection")
			}
			h.session.AbortRetry()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatal("prompt did not return after AbortRetry")
			}
			var finalErrors []string
			for _, end := range eventsOf[agent.AutoRetryEndEvent](log) {
				if end.Success {
					t.Fatalf("auto_retry_end reported success after the abort: %#v", end)
				}
				finalErrors = append(finalErrors, end.FinalError)
			}
			if !slices.Contains(finalErrors, "Retry cancelled") {
				t.Fatalf("auto_retry_end final errors = %v", finalErrors)
			}
			if got := requests.Load(); got != 1 {
				t.Fatalf("server requests = %d, want 1: the aborted backoff must not send a retry", got)
			}
		})
	}
}

// A connection the server itself closes (no gateway error event) is retried through the same session path: the transport error text is
// the Node counterpart's, which the classifier already matched before #12093.
func TestSessionRetriesAServerSocketClosedBeforeTheAnswer(t *testing.T) {
	for _, api := range closedConnectionAPIs() {
		t.Run(api.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					hijacker, ok := w.(http.Hijacker)
					if !ok {
						t.Error("test server does not support hijacking")
						return
					}
					connection, buffered, err := hijacker.Hijack()
					if err != nil {
						t.Errorf("hijack: %v", err)
						return
					}
					// Headers and one partial record, then the socket closes mid-stream.
					_, _ = buffered.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 4096\r\n\r\n")
					_, _ = buffered.WriteString(strings.SplitN(api.okBody, "\n\n", 2)[0] + "\n\n")
					_ = buffered.Flush()
					_ = connection.Close()
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
				t.Fatalf("server requests = %d, want 2", got)
			}
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
				t.Fatalf("retry events = %v", got)
			}
			if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || !strings.Contains(starts[0].ErrorMessage, "terminated") {
				t.Fatalf("auto_retry_start = %#v, want the mid-stream socket close reported as terminated", starts)
			}
			if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
				t.Fatalf("last assistant text = %v", text)
			}
		})
	}
}
