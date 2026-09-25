package ai

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
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

func runBedrockEvents(t *testing.T, events ...btypes.ConverseStreamOutput) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	input := make(chan btypes.ConverseStreamOutput, len(events))
	for _, event := range events {
		input <- event
	}
	close(input)
	builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "model")
	provider := &BedrockProvider{}
	go provider.parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: input}, builder)
	result := builder.stream.Result()
	var got []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
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
	wantContent := []AssistantContentBlock{
		TextContent{Text: "answer"},
		ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{"path": "main.go"}},
	}
	if !reflect.DeepEqual(result.Content, wantContent) || result.StopReason != StopReasonToolUse || result.RawStopReason != "tool_use" {
		t.Fatalf("result = %#v", result)
	}
	// Upstream handleMetadata: totalTokens falls back to input + output only.
	if result.Usage.Input != 10 || result.Usage.Output != 5 || result.Usage.CacheRead != 2 || result.Usage.CacheWrite != 3 || result.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	wantTypes := []AssistantEventType{
		EventStart,
		EventTextStart, EventTextDelta, EventTextEnd,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventDone,
	}
	var types []AssistantEventType
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
	if result.StopReason != StopReasonError || result.ErrorMessage != "amazon-bedrock: Unexpected assistant message start but got user message start instead" {
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
	if result.StopReason != StopReasonError || result.ErrorMessage != "amazon-bedrock: Bedrock stream ended without a stop reason" {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.Input != 7 || result.Usage.Output != 2 || result.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %#v, want input 7 output 2 total 9", result.Usage)
	}
	if got[len(got)-1].EventType() != EventError {
		t.Fatalf("terminal event = %s", got[len(got)-1].EventType())
	}
}
