//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"

	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

type fakeBedrockEventStream struct {
	events chan btypes.ConverseStreamOutput
	err    error
}

func (stream *fakeBedrockEventStream) Events() <-chan btypes.ConverseStreamOutput {
	return stream.events
}
func (stream *fakeBedrockEventStream) Close() error { return nil }
func (stream *fakeBedrockEventStream) Err() error   { return stream.err }

func runBedrockEvents(t *testing.T, events ...btypes.ConverseStreamOutput) (*ai.AssistantMessage, []ai.AssistantMessageEvent) {
	t.Helper()
	input := make(chan btypes.ConverseStreamOutput, len(events))
	for _, event := range events {
		input <- event
	}
	close(input)
	builder := ai.NewAssistantStreamBuilder(context.Background(), ai.APIBedrockConverseStream, "amazon-bedrock", "model")
	provider := &BedrockProvider{}
	go provider.parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: input}, builder, "")
	result := builder.Stream().Result()
	var got []ai.AssistantMessageEvent
	for event := range builder.Stream().Events(context.Background()) {
		got = append(got, event)
	}
	return result, got
}

func TestBedrockStreamPreservesSequenceUsageAndRawStopReason(t *testing.T) {
	result, got := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberText{Value: "answer"},
		}},
		&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
		&btypes.ConverseStreamOutputMemberContentBlockStart{Value: btypes.ContentBlockStartEvent{
			ContentBlockIndex: aws.Int32(1), Start: &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{ToolUseId: aws.String("call-1"), Name: aws.String("read")}},
		}},
		&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(1), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(`{"path":"main.go"}`)}},
		}},
		&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(1)}},
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonToolUse}},
		&btypes.ConverseStreamOutputMemberMetadata{Value: btypes.ConverseStreamMetadataEvent{Usage: &btypes.TokenUsage{
			InputTokens: aws.Int32(10), OutputTokens: aws.Int32(5), CacheReadInputTokens: aws.Int32(2), CacheWriteInputTokens: aws.Int32(3),
		}}},
	)
	wantContent := []ai.AssistantContentBlock{
		ai.TextContent{Text: "answer"},
		ai.ToolCall{ID: "call-1", Name: "read", Arguments: ai.JsonObject{"path": "main.go"}},
	}
	if !reflect.DeepEqual(result.Content, wantContent) || result.StopReason != ai.StopReasonToolUse || result.RawStopReason != "tool_use" {
		t.Fatalf("result = %#v", result)
	}
	// Upstream handleMetadata: totalTokens falls back to input + output only.
	if result.Usage.Input != 10 || result.Usage.Output != 5 || result.Usage.CacheRead != 2 || result.Usage.CacheWrite != 3 || result.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	wantTypes := []ai.AssistantEventType{
		ai.EventStart,
		ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd,
		ai.EventToolCallStart, ai.EventToolCallDelta, ai.EventToolCallEnd,
		ai.EventDone,
	}
	var types []ai.AssistantEventType
	for _, event := range got {
		types = append(types, event.EventType())
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Fatalf("event types = %v, want %v", types, wantTypes)
	}
}

func TestBedrockRejectsUserMessageStart(t *testing.T) {
	result, _ := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleUser}},
	)
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Unexpected assistant message start but got user message start instead" {
		t.Fatalf("result = %#v", result)
	}
}

func TestBedrockStreamWithoutStopReasonTerminatesWithError(t *testing.T) {
	result, got := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{
			ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberText{Value: "partial"},
		}},
		&btypes.ConverseStreamOutputMemberMetadata{Value: btypes.ConverseStreamMetadataEvent{Usage: &btypes.TokenUsage{
			InputTokens: aws.Int32(7), OutputTokens: aws.Int32(2),
		}}},
	)
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Bedrock stream ended without a stop reason" {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.Input != 7 || result.Usage.Output != 2 || result.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %#v, want input 7 output 2 total 9", result.Usage)
	}
	if got[len(got)-1].EventType() != ai.EventError {
		t.Fatalf("terminal event = %s", got[len(got)-1].EventType())
	}
}

// The SDK reader yields an event frame whose :event-type the Go union does not model as *types.UnknownUnionMember. Pi's union models the five
// exception members, so it forwards those items to onProviderStreamEvent and throws them (bedrock-converse-stream.ts:297,318-327), and drops any
// other unknown event before its loop (SmithyMessageDecoderStream.js drops a `$unknown` result).
func TestBedrockSDKStreamForwardsExceptionMembersAndDropsUnknownEvents(t *testing.T) {
	run := func(t *testing.T, events ...btypes.ConverseStreamOutput) (*ai.AssistantMessage, []any) {
		t.Helper()
		input := make(chan btypes.ConverseStreamOutput, len(events))
		for _, event := range events {
			input <- event
		}
		close(input)
		recorder := &providerEventRecorder{}
		builder := ai.NewAssistantStreamBuilder(context.Background(), ai.APIBedrockConverseStream, "amazon-bedrock", "model")
		builder.SetProviderEventObserver(ai.StreamOptions{OnProviderStreamEvent: recorder.observe}, &ai.Model{ID: "model"})
		go (&BedrockProvider{}).parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: input}, builder, "")
		result := builder.Stream().Result()
		observed, _ := recorder.snapshot()
		return result, observed
	}
	start := &btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}}

	exception := &btypes.UnknownUnionMember{Tag: "throttlingException", Value: []byte(`{"message":"slow down"}`)}
	result, observed := run(t, start, exception)
	if !reflect.DeepEqual(observed, []any{start, exception}) {
		t.Fatalf("observed = %#v", observed)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Throttling error: slow down" {
		t.Fatalf("result = %#v", result)
	}

	unknown := &btypes.UnknownUnionMember{Tag: "somethingUnknown", Value: []byte(`{}`)}
	result, observed = run(t, start, unknown)
	if !reflect.DeepEqual(observed, []any{start}) {
		t.Fatalf("observed = %#v", observed)
	}
	if result.StopReason != ai.StopReasonError || result.ErrorMessage != "Bedrock stream ended without a stop reason" {
		t.Fatalf("result = %#v", result)
	}
}
