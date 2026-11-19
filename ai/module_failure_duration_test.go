package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// moduleFailureProvider is a provider whose Stream returns an error before any stream exists, so that apiModuleFailure builds the failed message of its API module.
type moduleFailureProvider struct {
	name string
	api  API
	// failure builds the provider and the options of a call that fails after d: through the transport for a provider that makes its request inside Stream, through the payload hook for one that fails
	// before its request, and at once for one that only rejects the transcript (instant, which the test calls with d = 0).
	failure func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions)
	instant bool
}

func moduleFailureTranscript() TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
}

// moduleFailureFetch answers every request with the status after d.
func moduleFailureFetch(d time.Duration, status int) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		time.Sleep(d)
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"failed"}}`)), ProtoMajor: 1, ProtoMinor: 1}, nil
	})}
}

// moduleFailurePayload is a payload hook that rejects the request after d.
func moduleFailurePayload(d time.Duration) func(any, *Model) (any, error) {
	return func(any, *Model) (any, error) {
		time.Sleep(d)
		return nil, errors.New("payload rejected")
	}
}

func moduleFailureDuration(message *AssistantMessage) string {
	if message.DurationMs == nil {
		return "absent"
	}
	return strconv.FormatInt(*message.DurationMs, 10)
}

func moduleFailureRejectedTranscript() TranscriptContext {
	return TranscriptContext{err: errors.New("transcript rejected")}
}

func moduleFailureBuilt(t *testing.T, api API, provider Provider, err error) Provider {
	t.Helper()
	if err != nil {
		if pigstrip.Has(pigstrip.ListAPIs, string(api)) {
			t.Skipf("this build strips %s: %v", api, err)
		}
		t.Fatal(err)
	}
	return provider
}

// moduleFailureProviders is every built-in provider of package ai whose Stream returns an error before it has a stream: the ones that make their request inside Stream (google, google-vertex, mistral),
// the ones whose request starts after the stream exists but which reject a payload hook or a transcript before it (openai-completions, openai-responses, azure-openai-responses, openai-codex-responses, anthropic, pi-messages).
// amazon-bedrock has the same two early returns (an invalid transcript and a failed client) in its own package.
var moduleFailureProviders = []moduleFailureProvider{
	{name: "google-generative-ai", api: APIGoogleGenerativeAI, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider := NewGoogleProvider(GoogleConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureTranscript(), StreamOptions{Fetch: moduleFailureFetch(d, http.StatusBadRequest)}
	}},
	{name: "google-vertex", api: APIGoogleVertex, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider, err := NewGoogleVertexAPIProvider(GoogleVertexConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
		return moduleFailureBuilt(t, APIGoogleVertex, provider, err), moduleFailureTranscript(), StreamOptions{Fetch: moduleFailureFetch(d, http.StatusBadRequest)}
	}},
	{name: "mistral-conversations", api: APIMistralConversations, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider, err := NewMistralAPIProvider(MistralConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
		return moduleFailureBuilt(t, APIMistralConversations, provider, err), moduleFailureTranscript(), StreamOptions{Fetch: moduleFailureFetch(d, http.StatusInternalServerError)}
	}},
	{name: "openai-completions", api: APIOpenAICompletions, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider := NewOpenAIProvider(OpenAIConfig{BaseURL: "http://provider.invalid/v1", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureTranscript(), StreamOptions{OnPayload: moduleFailurePayload(d)}
	}},
	{name: "openai-responses", api: APIOpenAIResponses, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: "http://provider.invalid/v1", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureTranscript(), StreamOptions{OnPayload: moduleFailurePayload(d)}
	}},
	{name: "azure-openai-responses", api: APIAzureOpenAIResponses, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{BaseURL: "http://provider.invalid/openai/v1", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureTranscript(), StreamOptions{OnPayload: moduleFailurePayload(d)}
	}},
	{name: "openai-codex-responses", api: APIOpenAICodexResponses, instant: true, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		// The key is not a token with an account id, which the module rejects before it calls the hook.
		provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureTranscript(), StreamOptions{}
	}},
	{name: "anthropic-messages", api: APIAnthropicMessages, instant: true, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider := NewAnthropicProvider(AnthropicConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureRejectedTranscript(), StreamOptions{}
	}},
	{name: "pi-messages", api: APIPiMessages, instant: true, failure: func(t *testing.T, d time.Duration) (Provider, TranscriptContext, StreamOptions) {
		provider := NewPiMessagesProvider(PiMessagesConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
		return provider, moduleFailureRejectedTranscript(), StreamOptions{}
	}},
}

// moduleFailureTimings are the calls a provider's failure is measured with. Inside a synctest bubble the clock moves only when the call sleeps, so each wanted value is exact: `offset` is how far into a millisecond the call begins, `took` how long it lasts,
// and want is Pi's Math.round(performance.now() - startedAtMonotonic) of that.
var moduleFailureTimings = []struct {
	name         string
	offset, took time.Duration
	want         int64
}{
	{"no time passes", 0, 0, 0},
	{"within one millisecond", 200 * time.Microsecond, 400 * time.Microsecond, 0},
	{"across a millisecond tick", 300 * time.Microsecond, time.Millisecond, 1},
	{"one and a half milliseconds rounds up", 0, 1500 * time.Microsecond, 2},
	{"5 ms", 0, 5 * time.Millisecond, 5},
	{"40 ms", 700 * time.Microsecond, 40 * time.Millisecond, 40},
}

func moduleFailureModel(api API) *Model {
	return &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: "custom", API: api}}
}

// A provider whose call fails before it has a stream ends the module's stream with the module's failed message, which Pi times from the call: the module creates its stream first and stamps its message after (event-stream.ts:97-99, :126-128,
// google-generative-ai.ts:64, :83, :288-292). The message carries the timestamp of the call and the milliseconds the call took, including when the call began and ended in the same millisecond and when a millisecond tick fell inside it.
func TestAPIModuleFailureIsTimedFromTheCall(t *testing.T) {
	for _, tc := range moduleFailureProviders {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				for _, timing := range moduleFailureTimings {
					if tc.instant && timing.took != 0 {
						continue
					}
					provider, transcript, options := tc.failure(t, timing.took)
					time.Sleep(timing.offset)
					started := time.Now()
					stream, err := APIModuleStream(context.Background(), moduleFailureModel(tc.api), func(ctx context.Context) (*AssistantMessageEventStream, error) {
						return provider.Stream(ctx, transcript, options)
					})
					if err != nil {
						t.Fatalf("%s: APIModuleStream returned %v, want the module's failed stream", timing.name, err)
					}
					result := stream.Result()
					if result.StopReason != StopReasonError {
						t.Fatalf("%s: stop reason = %q (%s), want a failed call", timing.name, result.StopReason, result.ErrorMessage)
					}
					if result.Timestamp != started.UnixMilli() {
						t.Fatalf("%s: timestamp = %d, want the call's %d", timing.name, result.Timestamp, started.UnixMilli())
					}
					if result.DurationMs == nil || *result.DurationMs != timing.want {
						t.Fatalf("%s: durationMs = %s, want %d", timing.name, moduleFailureDuration(result), timing.want)
					}
					_ = provider.Close()
				}
			})
		})
	}
}

// The coding session forwards the module's stream through LazyStream (coding/model_stream_forwarding.go). A message that already has a durationMs keeps it (event-stream.ts:127 `message.durationMs !== undefined`), so the forwarding stream,
// which exists before the module's call, does not time it again: a call that begins `lead` after the forwarding stream carries what the call took, not lead plus that.
func TestLazyStreamForwardsTheModuleFailureDurationUnchanged(t *testing.T) {
	const lead = 7 * time.Millisecond
	for _, tc := range moduleFailureProviders {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				took := 40 * time.Millisecond
				if tc.instant {
					took = 0
				}
				provider, transcript, options := tc.failure(t, took)
				var started time.Time
				model := moduleFailureModel(tc.api)
				stream := LazyStream(context.Background(), model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
					time.Sleep(lead)
					started = time.Now()
					return APIModuleStream(ctx, model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
						return provider.Stream(ctx, transcript, options)
					})
				})
				result := stream.Result()
				if result.StopReason != StopReasonError {
					t.Fatalf("stop reason = %q (%s), want a failed call", result.StopReason, result.ErrorMessage)
				}
				if result.Timestamp != started.UnixMilli() {
					t.Fatalf("timestamp = %d, want the module call's %d", result.Timestamp, started.UnixMilli())
				}
				if want := took.Milliseconds(); result.DurationMs == nil || *result.DurationMs != want {
					t.Fatalf("durationMs = %s, want the module's %d (the forwarding stream began %v earlier)", moduleFailureDuration(result), want, lead)
				}
				_ = provider.Close()
			})
		})
	}
}
