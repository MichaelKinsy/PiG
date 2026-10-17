package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func errorMessageBeforeTimestamp(t *testing.T, stream *AssistantMessageEventStream) (bool, []byte) {
	t.Helper()
	result, err := stream.ResultContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	errorAt, timestampAt := bytes.Index(data, []byte(`"errorMessage"`)), bytes.Index(data, []byte(`"timestamp"`))
	if errorAt < 0 || timestampAt < 0 {
		t.Fatalf("message lacks errorMessage or timestamp: %s", data)
	}
	return errorAt < timestampAt, data
}

// lazy.ts createSetupErrorMessage builds { ..., stopReason, errorMessage, timestamp }: a Models request whose setup fails
// (a missing key, a failed OAuth refresh, a provider without the model's API) persists errorMessage before timestamp.
func TestModelsSetupErrorListsErrorMessageBeforeTimestamp(t *testing.T) {
	model := &Model{ID: "m1", ProviderMeta: ProviderMetadata{ProviderID: "demo", API: "openai-completions"}}
	stream := NewAssistantMessageEventStream()
	pushModelsSetupError(stream, model, errors.New(`Provider demo has no API implementation for "x"`))
	if errorFirst, data := errorMessageBeforeTimestamp(t, stream); !errorFirst {
		t.Fatalf("setup error message order: %s", data)
	}
}

// lazyStream's setup rejection (lazy.ts:46-60, for example azure.ts resolveAzureModel or a lazyApi load failure) is
// createSetupErrorMessage's message: errorMessage before timestamp.
func TestLazySetupRejectionListsErrorMessageBeforeTimestamp(t *testing.T) {
	model := &Model{ID: "m1", ProviderMeta: ProviderMetadata{ProviderID: "demo", API: "openai-responses"}}
	stream := LazyStream(context.Background(), model, func(context.Context) (*AssistantMessageEventStream, error) {
		return nil, errors.New("Azure OpenAI base URL is required.")
	})
	if errorFirst, data := errorMessageBeforeTimestamp(t, stream); !errorFirst {
		t.Fatalf("setup rejection order: %s", data)
	}
}

// A request failure that google-generative-ai.ts:288-298 catches inside the module's stream keeps the module's message: the
// output (timestamp included) exists before the catch sets errorMessage. Probed: Pi 1.1.0 google-vertex with an unreachable
// proxy persists "stopReason":"error","timestamp":…,"errorMessage":"fetch failed".
func TestGoogleRequestFailureListsTimestampBeforeErrorMessage(t *testing.T) {
	model := &Model{ID: "gemini-test", ProviderMeta: ProviderMetadata{ProviderID: "google", API: APIGoogleGenerativeAI, BaseURL: "http://127.0.0.1:1"}}
	stream, err := GoogleGenerativeAIAPI().Stream(context.Background(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	errorFirst, data := errorMessageBeforeTimestamp(t, stream)
	if errorFirst || !bytes.Contains(data, []byte(`"stopReason":"error"`)) || !bytes.Contains(data, []byte(`"errorMessage":"fetch failed"`)) {
		t.Fatalf("google request failure = %s, want stopReason error, timestamp before errorMessage \"fetch failed\"", data)
	}
}

// An API module's catch reports `options?.signal?.aborted ? "aborted" : "error"` (for example azure-openai-responses.ts:122),
// so a module failure under an aborted request is "aborted", unlike lazyStream's setup rejection (always "error").
func TestAPIModuleFailureUnderAnAbortedRequestIsAborted(t *testing.T) {
	model := &Model{ID: "m1", ProviderMeta: ProviderMetadata{ProviderID: "demo", API: APIOpenAIResponses}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := APIModuleStream(ctx, model, func(context.Context) (*AssistantMessageEventStream, error) {
		return nil, errors.New("Request was aborted.")
	})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonAborted || result.ErrorMessage != "Request was aborted." || result.ErrorBeforeTimestamp {
		t.Fatalf("result = %+v, want the module's aborted message, timestamp first", result)
	}
	if _, err := APIModuleStream(ctx, model, func(context.Context) (*AssistantMessageEventStream, error) {
		return nil, &lazySetupError{err: errors.New("setup")}
	}); err == nil || err.Error() != "setup" {
		t.Fatalf("setup error = %v, want it returned for lazyStream", err)
	}
}

// A module's streamSimple asserts the key before it calls its stream (openai-completions.ts:736-741 getClientApiKey,
// google-generative-ai.ts streamSimple), so a missing key rejects lazyApi's setup: errorMessage first. With a key it returns
// the module's stream, whose try catches the request failure: timestamp first, as the plain stream.
func TestSimpleStreamKeyAssertionIsSetupAndRequestFailureIsInModule(t *testing.T) {
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	google := &Model{ID: "gemini-test", ProviderMeta: ProviderMetadata{ProviderID: "google", API: APIGoogleGenerativeAI, BaseURL: "http://127.0.0.1:1"}}
	completions := &Model{ID: "m1", ProviderMeta: ProviderMetadata{ProviderID: "demo", API: APIOpenAICompletions, BaseURL: "http://127.0.0.1:1"}}
	cases := []struct {
		name           string
		stream         func() (*AssistantMessageEventStream, error)
		wantErrorFirst bool
		wantMessage    string
	}{
		{"google simple request failure", func() (*AssistantMessageEventStream, error) {
			return GoogleGenerativeAIAPI().StreamSimple(context.Background(), google, transcript, StreamOptions{APIKey: "k"})
		}, false, `"errorMessage":"fetch failed"`},
		{"openai-completions simple without a key", func() (*AssistantMessageEventStream, error) {
			return apiStreams(APIOpenAICompletions).StreamSimple(context.Background(), completions, transcript, StreamOptions{})
		}, true, `"errorMessage":"No API key for provider: demo"`},
		{"built-in provider's simple stream without a key", func() (*AssistantMessageEventStream, error) {
			return LazyStream(context.Background(), completions, func(ctx context.Context) (*AssistantMessageEventStream, error) {
				return builtinModuleStream(ctx, completions, transcript, StreamOptions{})
			}), nil
		}, true, `"errorMessage":"No API key for provider: demo"`},
		{"openai-completions stream without a key", func() (*AssistantMessageEventStream, error) {
			return apiStreams(APIOpenAICompletions).Stream(context.Background(), completions, transcript, StreamOptions{})
		}, false, `"errorMessage":"No API key for provider: demo"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stream, err := c.stream()
			if err != nil {
				t.Fatal(err)
			}
			errorFirst, data := errorMessageBeforeTimestamp(t, stream)
			if errorFirst != c.wantErrorFirst || !bytes.Contains(data, []byte(c.wantMessage)) || !bytes.Contains(data, []byte(`"stopReason":"error"`)) {
				t.Fatalf("errorMessage first = %v, want %v; want %s: %s", errorFirst, c.wantErrorFirst, c.wantMessage, data)
			}
		})
	}
}
