package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// failureTransport answers every request with one canned response. The body is built per request, so the provider can read it.
type failureTransport struct {
	status int
	header http.Header
	body   func() io.ReadCloser
}

func (transport failureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: transport.status, Status: http.StatusText(transport.status), Header: transport.header, Body: transport.body(), ProtoMajor: 1, ProtoMinor: 1}, nil
}

type erroringReader struct{ err error }

func (reader erroringReader) Read([]byte) (int, error) { return 0, reader.err }

// failedRequestProviders are the providers whose HTTP error path builds a response header map for the failure.
var failedRequestProviders = []struct {
	name  string
	build func() Provider
	// cut is the 200 body of a stream that delivers its first content and is then cut off.
	cut string
}{
	{"openai-completions", func() Provider {
		return NewOpenAIProvider(OpenAIConfig{BaseURL: "http://provider.invalid/v1", APIKey: "k", Model: "m", ProviderID: "custom"})
	}, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}]}\n\n"},
	{"openai-responses", func() Provider {
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: "http://provider.invalid/v1", APIKey: "k", Model: "m", ProviderID: "custom"})
	}, ""},
	{"anthropic", func() Provider {
		return NewAnthropicProvider(AnthropicConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
	}, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\n"},
	{"google", func() Provider {
		return NewGoogleProvider(GoogleConfig{BaseURL: "http://provider.invalid", APIKey: "k", Model: "m", ProviderID: "custom"})
	}, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hel\"}]}}]}\n\n"},
}

func rateLimitedTransport() failureTransport {
	return failureTransport{status: http.StatusTooManyRequests, header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"3"}},
		body: func() io.ReadCloser { return io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)) }}
}

func cutStreamTransport(prefix string) failureTransport {
	return failureTransport{status: http.StatusOK, header: http.Header{"Content-Type": {"text/event-stream"}},
		body: func() io.ReadCloser {
			return io.NopCloser(io.MultiReader(strings.NewReader(prefix), erroringReader{io.ErrUnexpectedEOF}))
		}}
}

// runFailedRequest sends one request that fails and returns its failed message. With record the request's context asks for the failure response, as the
// coding session's does when a retry decider is installed. The provider's stream is wrapped as the API module wrapper wraps it, which ends a
// stream that fails before it starts (the Google provider) with the module's failed message.
func runFailedRequest(t testing.TB, provider Provider, transport http.RoundTripper, record bool) *AssistantMessage {
	t.Helper()
	ctx := context.Background()
	if record {
		ctx = WithFailureResponse(ctx)
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: "custom", API: APIOpenAICompletions}}
	stream, err := APIModuleStream(ctx, model, func(ctx context.Context) (*AssistantMessageEventStream, error) {
		return provider.Stream(ctx, transcript, StreamOptions{Fetch: &http.Client{Transport: transport}})
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonError {
		t.Fatalf("stop reason = %q (%s), want a failed request", result.StopReason, result.ErrorMessage)
	}
	return result
}

// Without a decider (a context that did not come from WithFailureResponse) a failed request keeps no response metadata: stock builds no FailureResponse and no header map.
func TestFailedRequestWithoutRecordFailureResponseKeepsNoFailureResponse(t *testing.T) {
	for _, tc := range failedRequestProviders {
		t.Run(tc.name, func(t *testing.T) {
			if result := runFailedRequest(t, tc.build(), rateLimitedTransport(), false); result.FailureResponse != nil {
				t.Fatalf("FailureResponse = %#v, want nil", result.FailureResponse)
			}
		})
	}
}

// With a decider the response the provider saw reaches the failed message: status, lower-cased headers, and that no stream event came first.
func TestFailedRequestRecordsFailureResponseOnEveryHTTPPath(t *testing.T) {
	for _, tc := range failedRequestProviders {
		t.Run(tc.name, func(t *testing.T) {
			result := runFailedRequest(t, tc.build(), rateLimitedTransport(), true)
			failure := result.FailureResponse
			if failure == nil || failure.Status != http.StatusTooManyRequests || failure.Headers["retry-after"] != "3" || failure.StreamStarted {
				t.Fatalf("FailureResponse = %#v, want status 429, retry-after 3, stream not started", failure)
			}
		})
	}
}

// A stream that delivered its first event and was then cut off is StreamStarted: a decider must not retry it as it would a refused request.
func TestFailedRequestRecordsStreamStartedAfterACutStream(t *testing.T) {
	for _, tc := range failedRequestProviders {
		if tc.cut == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			result := runFailedRequest(t, tc.build(), cutStreamTransport(tc.cut), true)
			if failure := result.FailureResponse; failure == nil || !failure.StreamStarted || failure.Status != 0 {
				t.Fatalf("FailureResponse = %#v, want stream started and no status", failure)
			}
			if result := runFailedRequest(t, tc.build(), cutStreamTransport(tc.cut), false); result.FailureResponse != nil {
				t.Fatalf("without a decider FailureResponse = %#v, want nil", result.FailureResponse)
			}
		})
	}
}

// The failure response never leaves the process: the JSON of a failed message is the same with and without it.
func TestFailureResponseDoesNotSerialize(t *testing.T) {
	provider := failedRequestProviders[0].build()
	// The result is the stream's own message, which its producer may still be publishing: work on copies.
	plainResult, recordedResult := *runFailedRequest(t, provider, rateLimitedTransport(), false), *runFailedRequest(t, provider, rateLimitedTransport(), true)
	plain, recorded := &plainResult, &recordedResult
	if recorded.FailureResponse == nil {
		t.Fatal("precondition: no failure response recorded")
	}
	plain.Timestamp, recorded.Timestamp = 0, 0
	plain.DurationMs, recorded.DurationMs = nil, nil
	want, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(recorded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("JSON differs:\nrecorded %s\nplain    %s", got, want)
	}
	if string(got) != failedMessageJSONCar21 {
		t.Fatalf("JSON = %s\nwant car 21's %s", got, failedMessageJSONCar21)
	}
}

// failedMessageJSONCar21 is the JSON car 21 (d87c7d13e3) writes for the failed openai-completions message of this test, with the timestamp and the duration cleared.
const failedMessageJSONCar21 = `{"role":"assistant","content":[],"api":"openai-completions","provider":"custom","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","timestamp":0,"errorMessage":"429: {\"message\":\"rate limited\"}"}`

// The headers a failure reports are the ones of the response that had its status, and only a request that asked for them notes any.
func TestFailureResponseRecorderKeepsTheHeadersOfTheResponseThatFailed(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"3"}}}
	noteFailureResponse(context.Background(), response)
	if failureResponseRecorderOf(context.Background()) != nil {
		t.Fatal("a plain context grew a recorder")
	}
	ctx := WithFailureResponse(context.Background())
	recorder := failureResponseRecorderOf(ctx)
	if got := recorder.headersOf(http.StatusTooManyRequests); got != nil {
		t.Fatalf("headers before any response: %v", got)
	}
	noteFailureResponse(ctx, response)
	if got := recorder.headersOf(http.StatusTooManyRequests); got["retry-after"] != "3" {
		t.Fatalf("headers = %v, want retry-after 3", got)
	}
	if got := recorder.headersOf(http.StatusInternalServerError); got != nil {
		t.Fatalf("a failure with another status took the headers of the 429 response: %v", got)
	}
	if got := recorder.headersOf(0); got != nil {
		t.Fatalf("a failure with no status took headers: %v", got)
	}
}
