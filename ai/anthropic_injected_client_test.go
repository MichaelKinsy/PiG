package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// injectedAnthropicClient is the alternative SDK client of `AnthropicOptions.client` (anthropic-messages.ts:286-290), such as AnthropicVertex: it records the Messages call and answers with a canned stream.
type injectedAnthropicClient struct {
	mu       sync.Mutex
	calls    []AnthropicCreateParams
	options  []AnthropicRequestOptions
	failures []error
	sse      string
}

const injectedAnthropicSSE = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":3,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"from the injected client"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}

event: message_stop
data: {"type":"message_stop"}

`

func (c *injectedAnthropicClient) CreateMessage(_ context.Context, params AnthropicCreateParams, options AnthropicRequestOptions) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, params)
	c.options = append(c.options, options)
	if len(c.failures) > 0 {
		err := c.failures[0]
		c.failures = c.failures[1:]
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(c.sse))}, nil
}

func runInjectedAnthropic(t *testing.T, config AnthropicConfig, options StreamOptions) *AssistantMessage {
	t.Helper()
	p := NewAnthropicProvider(config)
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}})
	stream, err := p.Stream(t.Context(), transcript, options)
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

// upstream: packages/ai/src/api/anthropic-messages.ts:607-609 (`if (options?.client) { client = options.client; isOAuth = false; }` skips createClient, federation and assertRequestAuth) and :650 (the request goes through client.beta.messages.create).
func TestAnthropicInjectedClientReplacesClientConstructionAndAuth(t *testing.T) {
	var hits int
	srv := newUnreachableServer(t, &hits)
	client := &injectedAnthropicClient{sse: injectedAnthropicSSE}
	timeout := 1500
	// No API key and no auth header: without the injected client the provider fails with "No API key for provider: anthropic".
	message := runInjectedAnthropic(t, AnthropicConfig{Model: "claude-sonnet-4-20250514", BaseURL: srv, ProviderID: "anthropic"}, StreamOptions{
		MaxTokens: 64, AnthropicClient: client, TimeoutMs: &timeout,
		OnPayload: func(payload any, _ *Model) (any, error) {
			return map[string]any{"model": "replaced-by-hook", "max_tokens": 8, "betas": []string{"interleaved-thinking-2025-05-14"}, "messages": []any{}}, nil
		},
	})
	if message.StopReason != StopReasonStop || len(message.Content) != 1 || message.Content[0].(TextContent).Text != "from the injected client" {
		t.Fatalf("message = %+v, want the injected client's text", message)
	}
	if hits != 0 {
		t.Fatalf("the provider sent %d requests of its own, want none", hits)
	}
	if len(client.calls) != 1 {
		t.Fatalf("injected client calls = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if !strings.Contains(string(call.Body), `"model":"replaced-by-hook"`) || !strings.Contains(string(call.Body), `"stream":true`) || strings.Contains(string(call.Body), "betas") {
		t.Fatalf("params body = %s, want the payload hook's params with stream:true and no betas", call.Body)
	}
	if call.Betas == nil || *call.Betas != "interleaved-thinking-2025-05-14" {
		t.Fatalf("betas = %v, want the anthropic-beta value", call.Betas)
	}
	if client.options[0].TimeoutMs == nil || *client.options[0].TimeoutMs != 1500 {
		t.Fatalf("request options = %+v, want timeout 1500", client.options[0])
	}
}

// upstream: packages/ai/src/api/anthropic-messages.ts:614-615: without an injected client the request needs auth.
func TestAnthropicWithoutAnInjectedClientStillRequiresAuth(t *testing.T) {
	var hits int
	srv := newUnreachableServer(t, &hits)
	message := runInjectedAnthropic(t, AnthropicConfig{Model: "claude-sonnet-4-20250514", BaseURL: srv, ProviderID: "anthropic"}, StreamOptions{MaxTokens: 64})
	if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "No API key for provider: anthropic") {
		t.Fatalf("message = %q (%s), want the missing-key error", message.ErrorMessage, message.StopReason)
	}
}

// upstream: packages/ai/src/api/anthropic-messages.ts:650-658 retryProviderRequest wraps the injected client's call with options.maxRetries.
func TestAnthropicInjectedClientRetriesAProviderRequestError(t *testing.T) {
	var hits int
	srv := newUnreachableServer(t, &hits)
	retryAfter := http.Header{"Retry-After-Ms": {"1"}}
	status := http.StatusTooManyRequests
	failure := &testProviderError{status: &status, headers: retryAfter}
	client := &injectedAnthropicClient{sse: injectedAnthropicSSE, failures: []error{failure}}
	retries := 1
	message := runInjectedAnthropic(t, AnthropicConfig{Model: "claude-sonnet-4-20250514", BaseURL: srv, ProviderID: "anthropic"}, StreamOptions{MaxTokens: 64, AnthropicClient: client, MaxRetries: &retries})
	if message.StopReason != StopReasonStop || len(client.calls) != 2 {
		t.Fatalf("stop %s after %d calls (%s), want a retry then success", message.StopReason, len(client.calls), message.ErrorMessage)
	}
	// Without a retry budget the same failure is the message's error.
	client = &injectedAnthropicClient{sse: injectedAnthropicSSE, failures: []error{failure}}
	message = runInjectedAnthropic(t, AnthropicConfig{Model: "claude-sonnet-4-20250514", BaseURL: srv, ProviderID: "anthropic"}, StreamOptions{MaxTokens: 64, AnthropicClient: client})
	if message.StopReason != StopReasonError || len(client.calls) != 1 {
		t.Fatalf("stop %s after %d calls, want the failure without a retry", message.StopReason, len(client.calls))
	}
}

// newUnreachableServer is a base URL whose server counts every request it receives.
func newUnreachableServer(t *testing.T, hits *int) string {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		*hits++
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// upstream: packages/ai/src/api/anthropic-messages.ts:607-609 sets isOAuth = false for an injected client, so an OAuth-shaped key configured on
// the provider does not add the Claude Code identity to the params the client receives.
func TestAnthropicInjectedClientRequestIsNeverAnOAuthRequest(t *testing.T) {
	client := &injectedAnthropicClient{sse: injectedAnthropicSSE}
	message := runInjectedAnthropic(t, AnthropicConfig{Model: "claude-sonnet-4-20250514", ProviderID: "anthropic", APIKey: "sk-ant-oat01-configured"}, StreamOptions{MaxTokens: 64, AnthropicClient: client})
	if message.StopReason != StopReasonStop || len(client.calls) != 1 {
		t.Fatalf("stop %s after %d calls (%s)", message.StopReason, len(client.calls), message.ErrorMessage)
	}
	if body := string(client.calls[0].Body); strings.Contains(body, claudeCodeSystemPrompt) {
		t.Fatalf("params body = %s, want no Claude Code identity", body)
	}
	if betas := client.calls[0].Betas; betas != nil && strings.Contains(*betas, oauthBeta) {
		t.Fatalf("betas = %q, want no OAuth beta", *betas)
	}
}

type blockingAnthropicClient struct{ entered chan struct{} }

func (c *blockingAnthropicClient) CreateMessage(ctx context.Context, _ AnthropicCreateParams, _ AnthropicRequestOptions) (*http.Response, error) {
	close(c.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

// upstream: packages/ai/src/api/anthropic-messages.ts:644-658 passes options.signal to the client call and to retryProviderRequest, so an
// abort during the injected client's call ends the message as aborted.
func TestAnthropicInjectedClientCallObservesTheAbort(t *testing.T) {
	client := &blockingAnthropicClient{entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}})
	stream, err := NewAnthropicProvider(AnthropicConfig{Model: "claude-sonnet-4-20250514", ProviderID: "anthropic"}).Stream(ctx, transcript, StreamOptions{MaxTokens: 64, AnthropicClient: client})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.entered:
	case <-t.Context().Done():
		t.Fatal("the injected client was not called")
	}
	cancel()
	if message := stream.Result(); message.StopReason != StopReasonAborted {
		t.Fatalf("stop %s (%s), want aborted", message.StopReason, message.ErrorMessage)
	}
}
