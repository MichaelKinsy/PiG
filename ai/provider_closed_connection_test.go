package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Pi 9b88b94e2 (#12093, #12064). Pi matches the TEXT of an error produced by its JS SDKs; PiG's providers are Go over net/http and
// produce the same texts (ai/errors.go, transport_error.go map a Go network error to the undici/SDK wording). These tests pin, per API
// kind and credential shape, what the provider reports for the events a dropped connection can show, and that the classifier retries it.

type closedConnectionProvider struct {
	name string
	make func(baseURL string) Provider
	// gatewayEvent is the stream body of a gateway that ends the stream with "Connection closed.", and the error the provider reports.
	gatewayEvent string
	gatewayError string
	// dropped is the error the provider reports when the server closes the socket (before any byte, after the headers, mid-stream).
	beforeByte, afterHeaders, midStream string
}

func closedConnectionProviders() []closedConnectionProvider {
	const anthropicEvent = `{"type":"error","error":{"type":"api_error","message":"Connection closed."}}`
	const closedSocket = "terminated: other side closed (UND_ERR_SOCKET)"
	const connectionError = "Connection error.: other side closed (UND_ERR_SOCKET)"
	providers := []closedConnectionProvider{
		{
			name: "anthropic API key",
			make: func(u string) Provider {
				return NewAnthropicProvider(AnthropicConfig{BaseURL: u, APIKey: "test-key", Model: "claude-sonnet-4-20250514"})
			},
			gatewayEvent: "event: error\ndata: " + anthropicEvent + "\n\n", gatewayError: anthropicEvent,
			beforeByte: connectionError, afterHeaders: closedSocket, midStream: closedSocket,
		},
		{
			name: "anthropic OAuth token",
			make: func(u string) Provider {
				return NewAnthropicProvider(AnthropicConfig{BaseURL: u, APIKey: "sk-ant-oat-test-token", Model: "claude-sonnet-4-20250514"})
			},
			gatewayEvent: "event: error\ndata: " + anthropicEvent + "\n\n", gatewayError: anthropicEvent,
			beforeByte: connectionError, afterHeaders: closedSocket, midStream: closedSocket,
		},
		{
			name: "openai-completions custom base URL",
			make: func(u string) Provider {
				return NewOpenAIProvider(OpenAIConfig{BaseURL: u, APIKey: "test-key", Model: "gateway-model", ProviderID: "my-gateway"})
			},
			gatewayEvent: "data: {\"error\":{\"message\":\"Connection closed.\",\"type\":\"api_error\"}}\n\n", gatewayError: "Connection closed.",
			beforeByte: connectionError, afterHeaders: closedSocket, midStream: closedSocket,
		},
		{
			name: "openai-responses API key",
			make: func(u string) Provider {
				return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: u, APIKey: "test-key", Model: "gpt-test", ProviderID: "openai"})
			},
			gatewayEvent: "data: {\"type\":\"error\",\"code\":\"api_error\",\"message\":\"Connection closed.\"}\n\n", gatewayError: "Error Code api_error: Connection closed.",
			beforeByte: connectionError, afterHeaders: closedSocket, midStream: closedSocket,
		},
		{
			name: "openai-responses custom base URL",
			make: func(u string) Provider {
				return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: u, APIKey: "test-key", Model: "gateway-model", ProviderID: "my-gateway"})
			},
			gatewayEvent: "data: {\"type\":\"error\",\"code\":\"api_error\",\"message\":\"Connection closed.\"}\n\n", gatewayError: "Error Code api_error: Connection closed.",
			beforeByte: connectionError, afterHeaders: closedSocket, midStream: closedSocket,
		},
		{
			// Google's and Mistral's SDK wording differs: a fetch rejection reads "fetch failed", and neither SDK relays a stream error
			// event as an error message carrying the gateway's text, so there is no "Connection closed." event for them.
			name: "google",
			make: func(u string) Provider {
				return NewGoogleProvider(GoogleConfig{BaseURL: u, APIKey: "test-key", Model: "gemini-2.5-flash"})
			},
			beforeByte: "fetch failed", afterHeaders: closedSocket, midStream: closedSocket,
		},
	}
	// The Mistral case is in provider_closed_connection_mistral_test.go: a Piglet built with pig_strip_mistral_conversations has no
	// NewMistralProvider, and this file is built for every Piglet (make piglet-strip-build vets it with every strip tag).
	return append(providers, closedConnectionMistralProviders()...)
}

func TestProviderStreamErrorEventReadingConnectionClosedIsRetryable(t *testing.T) {
	for _, p := range closedConnectionProviders() {
		if p.gatewayEvent == "" {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(p.gatewayEvent))
			}))
			defer server.Close()
			result := transportErrorTestResult(t, p.make(server.URL))
			if result.StopReason != StopReasonError || result.ErrorMessage != p.gatewayError {
				t.Fatalf("result = reason %q error %q, want the provider's error %q", result.StopReason, result.ErrorMessage, p.gatewayError)
			}
			if !IsRetryableAssistantError(*result) {
				t.Fatalf("the gateway's closed connection %q is not retryable", result.ErrorMessage)
			}
		})
	}
}

// What PiG reports for a socket the server closes, per API kind, and that each is retried (it was before #12093: "other side closed",
// "terminated", "fetch failed" and "Connection error." are Pi's patterns).
func TestProviderServerSocketCloseIsRetryable(t *testing.T) {
	drops := []struct {
		name  string
		write func(w interface{ WriteString(string) (int, error) })
		want  func(closedConnectionProvider) string
	}{
		{"before any byte", func(interface{ WriteString(string) (int, error) }) {}, func(p closedConnectionProvider) string { return p.beforeByte }},
		{"after the headers", func(w interface{ WriteString(string) (int, error) }) {
			_, _ = w.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 4096\r\n\r\n")
		}, func(p closedConnectionProvider) string { return p.afterHeaders }},
		{"mid SSE stream", func(w interface{ WriteString(string) (int, error) }) {
			_, _ = w.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 4096\r\n\r\n")
			_, _ = w.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		}, func(p closedConnectionProvider) string { return p.midStream }},
	}
	for _, p := range closedConnectionProviders() {
		for _, drop := range drops {
			t.Run(p.name+"/"+drop.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					connection, buffered := closeHTTPConnection(t, w)
					drop.write(buffered)
					_ = buffered.Flush()
					_ = connection.Close()
				}))
				defer server.Close()
				result := transportErrorTestResult(t, p.make(server.URL))
				if result.StopReason != StopReasonError || result.ErrorMessage != drop.want(p) {
					t.Fatalf("result = reason %q error %q, want error %q", result.StopReason, result.ErrorMessage, drop.want(p))
				}
				if !IsRetryableAssistantError(*result) {
					t.Fatalf("a socket the server closed (%s) is not retryable: %q", drop.name, result.ErrorMessage)
				}
			})
		}
	}
}

// RetryAssistantCall over a real provider and a hermetic server. Contract: the caller waits for the whole cycle; the failed attempt is
// reported through OnRetryScheduled before the backoff and OnRetryAttemptStart after it; a context cancelled during the backoff returns
// the failed message as aborted without an error message and sends no further request; after MaxRetries retries the last error message is
// returned, not a Go error.
func TestRetryAssistantCallOverAGatewayClosedConnection(t *testing.T) {
	policy := RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 1}
	for _, p := range closedConnectionProviders() {
		if p.gatewayEvent == "" {
			continue
		}
		serve := func(t *testing.T) (*httptest.Server, *atomic.Int32) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(p.gatewayEvent))
			}))
			t.Cleanup(server.Close)
			return server, &requests
		}
		produce := func(t *testing.T, provider Provider) func() (AssistantMessage, error) {
			return func() (AssistantMessage, error) { return *transportErrorTestResult(t, provider), nil }
		}
		t.Run(p.name+"/one retry then an answer", func(t *testing.T) {
			// The first attempt is the provider's closed-connection error from the hermetic server; the second the server's answer, which the
			// per-API session test (coding/session_connection_closed_retry_test.go) serves for real.
			server, requests := serve(t)
			provider := p.make(server.URL)
			var calls int
			var scheduled []string
			response, err := RetryAssistantCall(t.Context(), func() (AssistantMessage, error) {
				calls++
				if calls == 1 {
					return *transportErrorTestResult(t, provider), nil
				}
				return retryFaux("answer", StopReasonStop, ""), nil
			}, &policy, RetryCallbacks{
				OnRetryScheduled: func(attempt, maxAttempts, delayMs int, errorMessage string) error {
					scheduled = append(scheduled, errorMessage)
					if attempt != 1 || maxAttempts != 2 || delayMs != 1 {
						t.Errorf("scheduled attempt=%d max=%d delay=%d", attempt, maxAttempts, delayMs)
					}
					return nil
				},
			})
			if err != nil || response.StopReason != StopReasonStop || calls != 2 || requests.Load() != 1 || len(scheduled) != 1 || scheduled[0] != p.gatewayError {
				t.Fatalf("response=%#v err=%v calls=%d requests=%d scheduled=%q", response, err, calls, requests.Load(), scheduled)
			}
		})
		t.Run(p.name+"/always closed", func(t *testing.T) {
			server, requests := serve(t)
			response, err := RetryAssistantCall(t.Context(), produce(t, p.make(server.URL)), &policy, RetryCallbacks{})
			if err != nil || response.StopReason != StopReasonError || response.ErrorMessage != p.gatewayError || requests.Load() != 3 {
				t.Fatalf("response=%#v err=%v requests=%d, want the surfaced error after 3 requests", response, err, requests.Load())
			}
		})
		t.Run(p.name+"/cancelled during the backoff", func(t *testing.T) {
			server, requests := serve(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			slow := RetryPolicy{Enabled: true, MaxRetries: 2, BaseDelayMs: 60_000}
			var finished []bool
			scheduled := make(chan struct{})
			go func() { <-scheduled; cancel() }()
			response, err := RetryAssistantCall(ctx, produce(t, p.make(server.URL)), &slow, RetryCallbacks{
				OnRetryScheduled: func(int, int, int, string) error { close(scheduled); return nil },
				OnRetryFinished:  func(success bool, _ int, _ string) error { finished = append(finished, success); return nil },
			})
			if err != nil || response.StopReason != StopReasonAborted || response.ErrorMessage != "" || requests.Load() != 1 || len(finished) != 1 || finished[0] {
				t.Fatalf("response=%#v err=%v requests=%d finished=%v, want an aborted message after one request", response, err, requests.Load(), finished)
			}

		})
	}
}
