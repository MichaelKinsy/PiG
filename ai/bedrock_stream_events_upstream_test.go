//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// .upstream/v0.99.1/packages/ai/test/bedrock-raw-stop-reason.test.ts:73,91
func TestBedrockProviderStreamEventsUpstream(t *testing.T) {
	generated := mustGeneratedModel(t, "amazon-bedrock", "us.anthropic.claude-opus-4-8").ToModel()
	newProvider := func(t *testing.T, frames [][2]string) (Provider, StreamOptions, *Model) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			writeBedrockFrames(t, w, frames)
		}))
		t.Cleanup(server.Close)
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
		t.Setenv("AWS_PROFILE", "")
		t.Setenv("AWS_DEFAULT_PROFILE", "")
		t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
		model := *generated
		model.ProviderMeta.BaseURL = server.URL
		provider := NewBedrockProviderWithModel(model)
		t.Cleanup(func() { _ = provider.Close() })
		return provider, StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}, &model
	}

	t.Run("forwards SDK stream items in order before normalizing them", func(t *testing.T) {
		provider, options, model := newProvider(t, [][2]string{
			{"messageStart", `{"role":"assistant"}`},
			{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"hello"}}`},
			{"messageStop", `{"stopReason":"end_turn","additionalModelResponseFields":{"source":"test"}}`},
			{"metadata", `{"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`},
		})
		recorder := &providerEventRecorder{}
		options.OnProviderStreamEvent = recorder.observe
		stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		events, models := recorder.snapshot()
		var kinds []string
		for _, event := range events {
			kinds = append(kinds, fmt.Sprintf("%T", event))
		}
		want := []string{
			"*types.ConverseStreamOutputMemberMessageStart", "*types.ConverseStreamOutputMemberContentBlockDelta",
			"*types.ConverseStreamOutputMemberMessageStop", "*types.ConverseStreamOutputMemberMetadata",
		}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("items = %v", kinds)
		}
		if delta := events[1].(*btypes.ConverseStreamOutputMemberContentBlockDelta); delta.Value.Delta.(*btypes.ContentBlockDeltaMemberText).Value != "hello" {
			t.Fatalf("delta = %#v", delta)
		}
		if stop := events[2].(*btypes.ConverseStreamOutputMemberMessageStop); stop.Value.StopReason != btypes.StopReasonEndTurn || stop.Value.AdditionalModelResponseFields == nil {
			t.Fatalf("stop = %#v", stop)
		}
		assertEventModels(t, models, model, 4)
		if result.StopReason != StopReasonStop || len(result.Content) != 1 || result.Content[0] != (TextContent{Text: "hello"}) {
			t.Fatalf("result = %#v", result)
		}
	})

	// A frame whose :message-type is exception never becomes a stream item: getMessageUnmarshaller (@smithy/core event-streams
	// getUnmarshalledStream.js) throws the deserialized exception before the for-await body runs, so the observer sees only the items before it.
	t.Run("forwards SDK stream items before an exception frame ends the request", func(t *testing.T) {
		provider, options, _ := newProvider(t, [][2]string{
			{"messageStart", `{"role":"assistant"}`},
			{"!internalServerException", `{"message":"bedrock stream failed"}`},
		})
		recorder := &providerEventRecorder{}
		options.OnProviderStreamEvent = recorder.observe
		stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		events, _ := recorder.snapshot()
		if len(events) != 1 {
			t.Fatalf("items = %#v", events)
		}
		if _, ok := events[0].(*btypes.ConverseStreamOutputMemberMessageStart); !ok {
			t.Fatalf("item = %#v", events[0])
		}
		// formatBedrockError names the modeled exception; the upstream mock throws a plain Error, so its message has no prefix.
		if result.StopReason != StopReasonError || result.ErrorMessage != "Internal server error: bedrock stream failed" {
			t.Fatalf("result = %#v", result)
		}
	})

	// .upstream/v0.99.2/packages/ai/test/bedrock-raw-stop-reason.test.ts:91. An event frame whose :event-type is a modeled exception member is
	// deserialized as that union member and yielded ({ internalServerException } and its siblings), so Pi forwards it to onProviderStreamEvent and
	// then throws it (bedrock-converse-stream.ts:297,318-327). The upstream mock throws a plain Error; the real SDK throws the modeled exception,
	// whose message formatBedrockError prefixes. Probe of the pinned Pi package's streamBedrock against these frames: each kind below delivered
	// [messageStart, {<kind>}] and the listed error; an event type the union does not model was dropped and the stream ended without a stop reason.
	for _, tc := range []struct{ kind, message string }{
		{"internalServerException", "Internal server error: bedrock stream failed"},
		{"modelStreamErrorException", "Model stream error: bedrock stream failed"},
		{"validationException", "Validation error: bedrock stream failed"},
		{"throttlingException", "Throttling error: bedrock stream failed"},
		{"serviceUnavailableException", "Service unavailable: bedrock stream failed"},
	} {
		t.Run("forwards SDK error items before reporting them/"+tc.kind, func(t *testing.T) {
			body := `{"message":"bedrock stream failed"}`
			provider, options, _ := newProvider(t, [][2]string{{"messageStart", `{"role":"assistant"}`}, {tc.kind, body}})
			recorder := &providerEventRecorder{}
			options.OnProviderStreamEvent = recorder.observe
			stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			events, _ := recorder.snapshot()
			if len(events) != 2 {
				t.Fatalf("items = %#v", events)
			}
			if _, ok := events[0].(*btypes.ConverseStreamOutputMemberMessageStart); !ok {
				t.Fatalf("item 0 = %#v", events[0])
			}
			if item, ok := events[1].(*btypes.UnknownUnionMember); !ok || item.Tag != tc.kind || string(item.Value) != body {
				t.Fatalf("item 1 = %#v", events[1])
			}
			if result.StopReason != StopReasonError || result.ErrorMessage != tc.message {
				t.Fatalf("result = %#v", result)
			}
		})
	}

	t.Run("drops event types the stream union does not model", func(t *testing.T) {
		provider, options, _ := newProvider(t, [][2]string{{"messageStart", `{"role":"assistant"}`}, {"somethingUnknown", `{"message":"bedrock stream failed"}`}})
		recorder := &providerEventRecorder{}
		options.OnProviderStreamEvent = recorder.observe
		stream, err := provider.Stream(t.Context(), userTranscript("hello"), options)
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if events, _ := recorder.snapshot(); len(events) != 1 {
			t.Fatalf("items = %#v", events)
		}
		if result.StopReason != StopReasonError || result.ErrorMessage != "Bedrock stream ended without a stop reason" {
			t.Fatalf("result = %#v", result)
		}
	})
}

// writeBedrockFrames encodes AWS event-stream frames. A kind that starts with "!" is a modeled exception.
func writeBedrockFrames(t *testing.T, w io.Writer, frames [][2]string) {
	t.Helper()
	encoder := eventstream.NewEncoder()
	for _, frame := range frames {
		headers := eventstream.Headers{}
		if exception, ok := strings.CutPrefix(frame[0], "!"); ok {
			headers.Set(":message-type", eventstream.StringValue("exception"))
			headers.Set(":exception-type", eventstream.StringValue(exception))
		} else {
			headers.Set(":message-type", eventstream.StringValue("event"))
			headers.Set(":event-type", eventstream.StringValue(frame[0]))
		}
		headers.Set(":content-type", eventstream.StringValue("application/json"))
		if err := encoder.Encode(w, eventstream.Message{Headers: headers, Payload: []byte(frame[1])}); err != nil {
			t.Fatal(err)
		}
	}
}
