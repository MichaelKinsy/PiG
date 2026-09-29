package ai

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func indexedBedrockText(index int32, text string) btypes.ConverseStreamOutput {
	return &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(index), Delta: &btypes.ContentBlockDeltaMemberText{Value: text}}}
}

func indexedBedrockStop(index int32) btypes.ConverseStreamOutput {
	return &btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(index)}}
}

func indexedBedrockEventTrace(t *testing.T, events []AssistantMessageEvent) []string {
	t.Helper()
	var trace []string
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			ContentIndex *int `json:"contentIndex"`
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		entry := string(event.EventType())
		if fields.ContentIndex != nil {
			entry += ":" + strconv.Itoa(*fields.ContentIndex)
		}
		trace = append(trace, entry)
	}
	return trace
}

// packages/ai/src/api/bedrock-converse-stream.ts:602-630,737-764 correlates by provider index while retaining content insertion order, not index sorting.
func TestBedrockIndexedTextBlocks(t *testing.T) {
	result, events := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		indexedBedrockText(8, "A"), indexedBedrockText(3, "B"), indexedBedrockText(8, "1"), indexedBedrockText(3, "2"),
		indexedBedrockStop(3), indexedBedrockStop(8),
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}},
	)
	want := []AssistantContentBlock{TextContent{Text: "A1"}, TextContent{Text: "B2"}}
	if !reflect.DeepEqual(result.Content, want) || result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v; content = %#v, want %#v", result, result.Content, want)
	}
	wantTrace := []string{"start", "text_start:0", "text_delta:0", "text_start:1", "text_delta:1", "text_delta:0", "text_delta:1", "text_end:1", "text_end:0", "done"}
	if trace := indexedBedrockEventTrace(t, events); !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("events = %v, want %v", trace, wantTrace)
	}
}

// packages/ai/src/api/bedrock-converse-stream.ts:636-700 finalizes encrypted bytes per block, including streams with missing contentBlockStop events.
func TestBedrockIndexedRedactedBlocks(t *testing.T) {
	for _, stops := range []bool{true, false} {
		t.Run("explicit stops="+strconv.FormatBool(stops), func(t *testing.T) {
			input := []btypes.ConverseStreamOutput{
				&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
				bedrockRedactedDelta(8, []byte("fi")), bedrockRedactedDelta(3, []byte("second")), bedrockRedactedDelta(8, []byte("rst")),
			}
			if stops {
				input = append(input, indexedBedrockStop(3), indexedBedrockStop(8))
			}
			input = append(input, &btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}})
			result, events := runBedrockEvents(t, input...)
			want := []AssistantContentBlock{
				ThinkingContent{Thinking: "[Reasoning redacted]", ThinkingSignature: base64.StdEncoding.EncodeToString([]byte("first")), Redacted: true},
				ThinkingContent{Thinking: "[Reasoning redacted]", ThinkingSignature: base64.StdEncoding.EncodeToString([]byte("second")), Redacted: true},
			}
			if !reflect.DeepEqual(result.Content, want) || result.StopReason != StopReasonStop {
				t.Fatalf("result = %+v; content = %#v, want %#v", result, result.Content, want)
			}
			wantTrace := []string{"start", "thinking_start:0", "thinking_delta:0", "thinking_start:1", "thinking_delta:1"}
			if stops {
				wantTrace = append(wantTrace, "thinking_end:1", "thinking_end:0")
			}
			wantTrace = append(wantTrace, "done")
			if trace := indexedBedrockEventTrace(t, events); !reflect.DeepEqual(trace, wantTrace) {
				t.Fatalf("events = %v, want %v", trace, wantTrace)
			}
		})
	}
}

// packages/ai/src/api/bedrock-converse-stream.ts:648-658 keeps text and signatures in their indexed thinking block and ignores empty thinking text deltas.
func TestBedrockIndexedThinkingTextAndSignatures(t *testing.T) {
	text := func(index int32, value string) btypes.ConverseStreamOutput {
		return &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(index), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberText{Value: value}}}}
	}
	signature := func(index int32, value string) btypes.ConverseStreamOutput {
		return &btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(index), Delta: &btypes.ContentBlockDeltaMemberReasoningContent{Value: &btypes.ReasoningContentBlockDeltaMemberSignature{Value: value}}}}
	}
	result, events := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		text(8, ""), text(8, "first"), text(3, "second"), text(8, "-end"),
		signature(8, "sig-A"), signature(3, "sig-B"), indexedBedrockText(3, "ignored"),
		indexedBedrockStop(3), indexedBedrockStop(8),
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}},
	)
	want := []AssistantContentBlock{ThinkingContent{Thinking: "first-end", ThinkingSignature: "sig-A"}, ThinkingContent{Thinking: "second", ThinkingSignature: "sig-B"}}
	if !reflect.DeepEqual(result.Content, want) || result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v; content = %#v, want %#v", result, result.Content, want)
	}
	wantTrace := []string{"start", "thinking_start:0", "thinking_delta:0", "thinking_start:1", "thinking_delta:1", "thinking_delta:0", "thinking_end:1", "thinking_end:0", "done"}
	if trace := indexedBedrockEventTrace(t, events); !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("events = %v, want %v", trace, wantTrace)
	}
}

// packages/ai/src/api/bedrock-converse-stream.ts:368-377 removes streaming scratch at terminal paths without inventing contentBlockStop events.
func TestBedrockTerminalDoesNotInventToolStops(t *testing.T) {
	for _, tc := range []struct {
		raw      btypes.StopReason
		stop     StopReason
		terminal string
	}{
		{btypes.StopReasonToolUse, StopReasonToolUse, "done"},
		{btypes.StopReasonGuardrailIntervened, StopReasonError, "error"},
	} {
		t.Run(string(tc.raw), func(t *testing.T) {
			result, events := runBedrockEvents(t,
				&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
				&btypes.ConverseStreamOutputMemberContentBlockStart{Value: btypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(8), Start: &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{ToolUseId: aws.String("call-1"), Name: aws.String("read")}}}},
				&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(8), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(`{"path":"a"}`)}}}},
				&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: tc.raw}},
			)
			want := []AssistantContentBlock{ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{"path": "a"}}}
			if !reflect.DeepEqual(result.Content, want) || result.StopReason != tc.stop {
				t.Fatalf("result = %+v; content = %#v", result, result.Content)
			}
			wantTrace := []string{"start", "toolcall_start:0", "toolcall_delta:0", tc.terminal}
			if trace := indexedBedrockEventTrace(t, events); !reflect.DeepEqual(trace, wantTrace) {
				t.Fatalf("events = %v, want %v", trace, wantTrace)
			}
		})
	}
}

// packages/ai/src/api/bedrock-converse-stream.ts:621,626,648 applies a delta only when its member matches the indexed block kind.
func TestBedrockIndexedDeltaKindGuards(t *testing.T) {
	result, events := runBedrockEvents(t,
		&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
		indexedBedrockText(8, "text"), bedrockRedactedDelta(8, []byte("ignored")),
		&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(8), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(`{"ignored":true}`)}}}},
		indexedBedrockStop(8),
		&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonEndTurn}},
	)
	want := []AssistantContentBlock{TextContent{Text: "text"}}
	if !reflect.DeepEqual(result.Content, want) || result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v; content = %#v", result, result.Content)
	}
	wantTrace := []string{"start", "text_start:0", "text_delta:0", "text_end:0", "done"}
	if trace := indexedBedrockEventTrace(t, events); !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("events = %v, want %v", trace, wantTrace)
	}
}
