package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// The callback sites differ upstream: SDK withResponse paths expose success
// only (openai-completions.ts:378, openai-responses.ts:177, anthropic-messages.ts:595,
// azure-openai-responses.ts:130), raw fetch exposes errors too (pi-messages.ts:404,
// mistral-conversations.ts:311, openai-codex-responses.ts:414). Bedrock's
// deserialize middleware observes successful raw responses (lines 516-525).
func TestProviderResponseBoundaries(t *testing.T) {
	clearAnthropicAuthEnv(t)
	cases := []struct {
		name          string
		errorsVisible bool
		make          func(string) Provider
	}{
		{"completions", false, func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{APIKey: "key", Model: "probe", BaseURL: url})
		}},
		{"responses", false, func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "key", Model: "probe", BaseURL: url})
		}},
		{"azure", false, func(url string) Provider {
			return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "key", Model: "probe", BaseURL: url})
		}},
		{"anthropic", false, func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{APIKey: "key", Model: "probe", BaseURL: url})
		}},
		{"mistral", true, func(url string) Provider {
			return NewMistralProvider(MistralConfig{APIKey: "key", Model: "probe", BaseURL: url})
		}},
		{"pi-messages", true, func(url string) Provider {
			return NewPiMessagesProvider(PiMessagesConfig{APIKey: "key", Model: "probe", BaseURL: url})
		}},
		{"codex-sse", true, func(url string) Provider {
			return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acct"), Model: "probe", BaseURL: url})
		}},
		{"bedrock", false, func(url string) Provider { return NewBedrockProvider("probe", url) }},
	}
	for _, test := range cases {
		for _, status := range []int{200, 403} {
			t.Run(fmt.Sprintf("%s/%d", test.name, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
					w.Header().Add("X-Probe", "first")
					w.Header().Add("X-Probe", "second")
					w.Header().Add("Set-Cookie", "a=1")
					w.Header().Add("Set-Cookie", "b=2")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"message":"probe"}`)
				}))
				defer server.Close()
				provider := test.make(server.URL)
				defer func() { _ = provider.Close() }()
				var observations []ProviderResponse
				sentinel := errors.New("response observer rejected")
				options := StreamOptions{Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, OnResponse: func(ctx context.Context, response ProviderResponse, model *Model) error {
					if ctx == nil || model == nil || model.ID != "probe" {
						t.Errorf("callback context/model = %v/%#v", ctx, model)
					}
					observations = append(observations, response)
					return sentinel
				}}
				stream, err := provider.Stream(WithProviderMaxRetries(t.Context(), 0), piMessagesTestContext(), options)
				if stream != nil {
					result := stream.Result()
					if result != nil && result.StopReason == StopReasonError {
						err = errors.New(result.ErrorMessage)
					}
				}
				wantObserved := status == 200 || test.errorsVisible
				if !wantObserved {
					if len(observations) != 0 {
						t.Fatalf("error response observed: %v", observations)
					}
					return
				}
				if len(observations) != 1 || observations[0].Status != status || observations[0].Headers["x-probe"] != "first, second" || observations[0].Headers["set-cookie"] != "b=2" {
					t.Fatalf("response metadata = %#v", observations)
				}
				if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
					t.Fatalf("observer rejection = %v", err)
				}
			})
		}
	}
}

type unreadResponseBody struct{ reads, closes atomic.Int32 }

func (b *unreadResponseBody) Read([]byte) (int, error) { b.reads.Add(1); return 0, io.EOF }
func (b *unreadResponseBody) Close() error             { b.closes.Add(1); return nil }

// Rejection and cancellation happen while the body is still unread. There is no
// detached observer task or body-reader goroutine to outlive the request.
func TestProviderResponseAwaitCancellationAndCleanup(t *testing.T) {
	body := &unreadResponseBody{}
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "probe", BaseURL: "http://probe.invalid"}, client: &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	})}}
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		stream, err := provider.Stream(ctx, piMessagesTestContext(), StreamOptions{OnResponse: func(ctx context.Context, _ ProviderResponse, _ *Model) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}})
		finished <- awaitProviderFailure(t, stream, err)
	}()
	<-entered
	if body.reads.Load() != 0 {
		t.Fatal("body consumed before observer completion")
	}
	select {
	case err := <-finished:
		t.Fatalf("terminal result completed before observer: %v", err)
	default:
	}
	cancel()
	if err := <-finished; err == nil || err.Error() != context.Canceled.Error() {
		t.Fatalf("cancel = %v", err)
	}
	if body.reads.Load() != 0 || body.closes.Load() != 1 {
		t.Fatalf("body reads/closes = %d/%d", body.reads.Load(), body.closes.Load())
	}
}

func TestCodexResponseObservesEachRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		if attempts.Add(1) == 1 {
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "acct"), Model: "probe", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	var statuses []int
	stream, err := provider.Stream(WithProviderMaxRetries(t.Context(), 1), piMessagesTestContext(), StreamOptions{Transport: TransportSSE, OnResponse: func(_ context.Context, r ProviderResponse, _ *Model) error {
		statuses = append(statuses, r.Status)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	if !reflect.DeepEqual(statuses, []int{429, 200}) {
		t.Fatalf("responses = %v", statuses)
	}
}

// Ports packages/ai/test/bedrock-response-headers.test.ts:37.
func TestBedrockResponseHeadersUpstream(t *testing.T) {
	const modelID = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("x-bifrost-provider", "bedrock")
		w.Header().Set("x-bifrost-resolved-model", modelID)
		w.Header().Set("x-amzn-requestid", "req-123")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	model := mustGeneratedModel(t, "amazon-bedrock", modelID).ToModel()
	model.ProviderMeta.BaseURL = server.URL
	provider := NewBedrockProviderWithModel(*model)
	var responses []ProviderResponse
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{
		CacheRetention: CacheRetentionNone,
		Env:            ProviderEnv{"AWS_BEDROCK_FORCE_HTTP1": "1", "AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
		OnResponse: func(_ context.Context, response ProviderResponse, _ *Model) error {
			responses = append(responses, response)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonError {
		t.Fatalf("empty event stream result = %#v", result)
	}
	if len(responses) != 1 || responses[0].Status != 200 {
		t.Fatalf("responses = %#v", responses)
	}
	for name, want := range map[string]string{"x-amzn-requestid": "req-123", "x-bifrost-provider": "bedrock", "x-bifrost-resolved-model": modelID} {
		if got := responses[0].Headers[name]; got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
}

func BenchmarkProviderResponseObserver(b *testing.B) {
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"probe"}, "X-RateLimit-Remaining": {"120"}, "Set-Cookie": {"a=1", "b=2"}}}
	model := &Model{ID: "probe"}
	options := StreamOptions{OnResponse: func(context.Context, ProviderResponse, *Model) error { return nil }}
	b.ReportAllocs()
	for b.Loop() {
		if err := observeProviderResponse(b.Context(), options, response, model); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFauxResponseObservationPrecedesFactoryAndEmptyQueue(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	var order []string
	provider.SetResponses([]FauxResponseStep{{Factory: func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (FauxResponse, error) {
		order = append(order, "factory")
		return FauxResponse{}, nil
	}}})
	for range 2 {
		stream, err := provider.Stream(t.Context(), piMessagesTestContext(), StreamOptions{OnResponse: func(_ context.Context, r ProviderResponse, _ *Model) error {
			if r.Status != 200 || r.Headers == nil || len(r.Headers) != 0 {
				t.Errorf("faux response = %#v", r)
			}
			order = append(order, "response")
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.Result()
	}
	if !reflect.DeepEqual(order, []string{"response", "factory", "response"}) {
		t.Fatalf("order = %v", order)
	}
}
