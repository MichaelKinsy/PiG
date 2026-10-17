//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"net"
	"testing"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// TestBedrockStreamEventReadsIntegerValuedFloats pins JSON.parse number semantics for the Bedrock event-stream body: Pi's AWS SDK parses the payload with
// JSON.parse and reads integer members through smithy's expectInt32, which accepts any Number.isInteger value, so 0.0, 1e0 and 12.0 are the integers 0, 1
// and 12. encoding/json refuses them for an int32 field.
func TestBedrockStreamEventReadsIntegerValuedFloats(t *testing.T) {
	for _, body := range []string{`{"contentBlockIndex":2}`, `{"contentBlockIndex":2.0}`, `{"contentBlockIndex":2e0}`, `{"contentBlockIndex":20e-1}`} {
		item, err := bedrockStreamEvent("contentBlockStop", []byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		stop, ok := item.event.(*btypes.ConverseStreamOutputMemberContentBlockStop)
		if !ok || stop.Value.ContentBlockIndex == nil || *stop.Value.ContentBlockIndex != 2 {
			t.Fatalf("%s: event %#v, want content block 2", body, item.event)
		}
	}
	item, err := bedrockStreamEvent("metadata", []byte(`{"usage":{"inputTokens":12.0,"outputTokens":3e0,"totalTokens":15}}`))
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := item.event.(*btypes.ConverseStreamOutputMemberMetadata)
	if !ok || metadata.Value.Usage == nil || *metadata.Value.Usage.InputTokens != 12 || *metadata.Value.Usage.OutputTokens != 3 {
		t.Fatalf("metadata event %#v, want usage 12/3", item.event)
	}
	// A fraction is not an integer: smithy's expectInt32 throws, and the decoder still refuses it.
	if _, err := bedrockStreamEvent("contentBlockStop", []byte(`{"contentBlockIndex":2.5}`)); err == nil {
		t.Fatal("contentBlockIndex 2.5 decoded")
	}
}

// TestBedrockStreamAcceptsIntegerValuedFloats drives the same bodies through the Bedrock provider: before the fix the stream ended with the decoder's
// "cannot unmarshal number 0.0" error, where Pi streams the text and reports the usage.
func TestBedrockStreamAcceptsIntegerValuedFloats(t *testing.T) {
	var body []byte
	body = append(body, bedrockBenchFrame("messageStart", `{"role":"assistant"}`)...)
	body = append(body, bedrockBenchFrame("contentBlockDelta", `{"contentBlockIndex":0.0,"delta":{"text":"hi"}}`)...)
	body = append(body, bedrockBenchFrame("contentBlockStop", `{"contentBlockIndex":0e0}`)...)
	body = append(body, bedrockBenchFrame("messageStop", `{"stopReason":"end_turn"}`)...)
	body = append(body, bedrockBenchFrame("metadata", `{"usage":{"inputTokens":12.0,"outputTokens":3.0,"totalTokens":15}}`)...)
	server := newBedrockFixtureServer(t, nil, body, true)
	server.openOnce()
	_, port, _ := net.SplitHostPort(server.listener.Addr().String())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := bedrockObservationProvider(port).Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{
		APIKey: "test", Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(ctx) {
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
		t.Fatalf("stop %s error %q", result.StopReason, result.ErrorMessage)
	}
	if len(result.Content) != 1 || result.Usage.Input != 12 || result.Usage.Output != 3 {
		t.Fatalf("content %#v usage %+v", result.Content, result.Usage)
	}
	if text, ok := result.Content[0].(TextContent); !ok || text.Text != "hi" {
		t.Fatalf("content %#v, want the text hi", result.Content)
	}
}
