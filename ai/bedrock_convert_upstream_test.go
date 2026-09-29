package ai

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// This test value represents upstream's untyped {type:unknown,data:foo}; it is not a production union member.
type unknownBedrockContent struct{ Data string }

func (unknownBedrockContent) contentBlock()            {}
func (unknownBedrockContent) contentType() string      { return "unknown" }
func (unknownBedrockContent) isUserContentBlock()      {}
func (unknownBedrockContent) isAssistantContentBlock() {}

// packages/ai/test/bedrock-convert-messages.test.ts:85,253 captures the real provider with cacheRetention=none and expects only the retained hello text block.
func TestBedrockUpstreamConvertCallerHonorsCacheRetentionNone(t *testing.T) {
	isolateBedrockConfig(t)
	t.Setenv("PI_CACHE_RETENTION", "")
	model := cloneGeneratedModel(t, "amazon-bedrock/us.anthropic.claude-sonnet-4-5-20250929-v1:0").ToModel()
	provider := NewBedrockProviderWithModel(*model)
	captured := errors.New("payload captured")
	var messages []btypes.Message
	_, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserContentBlocks{TextContent{}, TextContent{Text: "hello"}}},
	}}), StreamOptions{CacheRetention: CacheRetentionNone, OnPayload: func(value any, _ *Model) (any, error) {
		messages = value.(*bedrockruntime.ConverseStreamInput).Messages
		return nil, captured
	}})
	if !errors.Is(err, captured) {
		t.Fatalf("Stream error = %v, want payload capture", err)
	}
	want := []btypes.Message{{Role: btypes.ConversationRoleUser, Content: []btypes.ContentBlock{
		&btypes.ContentBlockMemberText{Value: "hello"},
	}}}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("cacheRetention=none request messages = %#v, want only the hello text block", messages)
	}
}

func TestBedrockUpstreamConvertMessages(t *testing.T) {
	const model = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	assistant := func(content ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{API: APIBedrockConverseStream, Provider: "amazon-bedrock", Model: model, Content: content, StopReason: StopReasonStop}
	}
	for _, tc := range []struct {
		name    string
		message Message
		want    string
		empty   bool
	}{
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:178
		{"skips unknown user content blocks instead of throwing", UserMessage{Content: UserContentBlocks{TextContent{Text: "hello"}, unknownBedrockContent{Data: "foo"}}}, "hello", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:197
		{"skips unknown assistant content blocks instead of throwing", assistant(TextContent{Text: "hello"}, unknownBedrockContent{Data: "foo"}), "hello", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:228
		{"replaces user messages with only unknown content blocks with a placeholder", UserMessage{Content: UserContentBlocks{unknownBedrockContent{Data: "foo"}}}, "<empty>", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:243
		{"replaces blank user string content with a placeholder", UserMessage{Content: UserText("   ")}, "<empty>", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:253
		{"filters blank user text blocks when other content remains", UserMessage{Content: UserContentBlocks{TextContent{}, TextContent{Text: "hello"}}}, "hello", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:272
		// WTF-8 retains the exact lone UTF-16 high surrogate that String.fromCharCode(0xd83d) creates.
		{"replaces user content emptied by surrogate sanitization with a placeholder", UserMessage{Content: UserText("\xed\xa0\xbd")}, "<empty>", false},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:282
		{"skips assistant text blocks emptied by surrogate sanitization", assistant(TextContent{Text: "\xed\xa0\xbd"}), "", true},
		// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:328
		{"skips assistant messages with only unknown content blocks", assistant(unknownBedrockContent{Data: "foo"}), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := convertBedrockMessages([]Message{tc.message}, model, "Claude Sonnet 4.5 (US)", bedrockCacheNone, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.empty {
				if len(result) != 0 {
					t.Fatalf("messages=%#v", result)
				}
				return
			}
			if len(result) != 1 || len(result[0].Content) != 1 {
				t.Fatalf("messages=%#v", result)
			}
			text, ok := result[0].Content[0].(*btypes.ContentBlockMemberText)
			if !ok || text.Value != tc.want {
				t.Fatalf("content=%#v, want %q", result[0].Content, tc.want)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:308
	t.Run("replaces blank tool result content with a placeholder", func(t *testing.T) {
		messages, err := convertBedrockMessages([]Message{ToolResultMessage{ToolCallID: "tool-1", ToolName: "tool", Content: []ToolResultMessageContent{TextContent{}}, IsError: false}}, model, "Claude Sonnet 4.5 (US)", bedrockCacheNone, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 || len(messages[0].Content) != 1 {
			t.Fatalf("messages=%#v", messages)
		}
		result, ok := messages[0].Content[0].(*btypes.ContentBlockMemberToolResult)
		if !ok || len(result.Value.Content) != 1 {
			t.Fatalf("result=%#v", messages[0].Content[0])
		}
		text, ok := result.Value.Content[0].(*btypes.ToolResultContentBlockMemberText)
		if !ok || text.Value != "<empty>" {
			t.Fatalf("content=%#v", result.Value.Content)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:101
	t.Run("gates native strict tool use by model capability", func(t *testing.T) {
		tool := ToolSchema{Name: "lookup", Description: "Look up a value", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}}
		config, err := convertBedrockTools([]ToolSchema{tool}, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if config == nil || len(config.Tools) != 1 || !aws.ToBool(config.Tools[0].(*btypes.ToolMemberToolSpec).Value.Strict) {
			t.Fatalf("strict config=%#v", config)
		}
		tool.ConstrainedSampling = &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}
		config, err = convertBedrockTools([]ToolSchema{tool}, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if config == nil || len(config.Tools) != 1 || config.Tools[0].(*btypes.ToolMemberToolSpec).Value.Strict != nil {
			t.Fatalf("Nova strict config=%#v", config)
		}
	})
	const input = `{"path":"/workspace/foobar/file.js","edits":[{"oldText":"first","newText":"updated first"},{"oldText":"second","newText":"updated second","":""}]}`
	// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:129
	t.Run("preserves empty property names in streamed tool arguments", func(t *testing.T) {
		result, _ := runBedrockEvents(t,
			&btypes.ConverseStreamOutputMemberMessageStart{Value: btypes.MessageStartEvent{Role: btypes.ConversationRoleAssistant}},
			&btypes.ConverseStreamOutputMemberContentBlockStart{Value: btypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(0), Start: &btypes.ContentBlockStartMemberToolUse{Value: btypes.ToolUseBlockStart{ToolUseId: aws.String("tool-1"), Name: aws.String("edit")}}}},
			&btypes.ConverseStreamOutputMemberContentBlockDelta{Value: btypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &btypes.ContentBlockDeltaMemberToolUse{Value: btypes.ToolUseBlockDelta{Input: aws.String(input)}}}},
			&btypes.ConverseStreamOutputMemberContentBlockStop{Value: btypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}},
			&btypes.ConverseStreamOutputMemberMessageStop{Value: btypes.MessageStopEvent{StopReason: btypes.StopReasonToolUse}},
		)
		if len(result.Content) == 0 {
			t.Fatal("missing tool call")
		}
		encoded, err := json.Marshal(result.Content[0])
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, `{"type":"toolCall","id":"tool-1","name":"edit","arguments":`+input+`}`)
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-convert-messages.test.ts:354
	t.Run("removes empty property names only from replayed Bedrock input", func(t *testing.T) {
		var arguments JsonObject
		if err := json.Unmarshal([]byte(input), &arguments); err != nil {
			t.Fatal(err)
		}
		message := assistant(ToolCall{ID: "tool-1", Name: "edit", Arguments: arguments})
		message.StopReason = StopReasonToolUse
		messages, err := convertBedrockMessages([]Message{message, ToolResultMessage{ToolCallID: "tool-1", ToolName: "edit", Content: []ToolResultMessageContent{TextContent{Text: "done"}}}, UserMessage{Content: UserText("Continue")}}, model, "Claude Sonnet 4.5 (US)", bedrockCacheNone, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 3 || len(messages[0].Content) != 1 {
			t.Fatalf("messages=%#v", messages)
		}
		tool, ok := messages[0].Content[0].(*btypes.ContentBlockMemberToolUse)
		if !ok {
			t.Fatalf("tool=%#v", messages[0].Content[0])
		}
		encoded, err := tool.Value.Input.MarshalSmithyDocument()
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, `{"path":"/workspace/foobar/file.js","edits":[{"oldText":"first","newText":"updated first"},{"oldText":"second","newText":"updated second"}]}`)
		want := map[string]any{"oldText": "second", "newText": "updated second", "": ""}
		if !reflect.DeepEqual(arguments["edits"].([]any)[1], want) {
			t.Fatalf("caller arguments mutated: %#v", arguments)
		}
	})
}
