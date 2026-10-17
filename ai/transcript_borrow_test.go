package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// richTranscriptMessages is a history that reaches every rewrite a provider makes: a leading prompt with tools, mid-conversation system updates with tool changes (a strict JSON-schema tool included), images in user and tool result messages, signed, unsigned, redacted and blank thinking, same-model and foreign assistant turns, errored and aborted turns, parallel calls with a late and an orphan result, and a call stored without a Responses call id (D129).
// With removal set, the later system update removes a tool and redefines it (a non-additive change that folds the request tools); without it, the update only adds a tool, which a provider that anchors additions sends in place.
func richTranscriptMessages(model *GeneratedModel, removal bool) []Message {
	same := func(blocks ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{API: model.API, Provider: model.Provider, Model: model.ID, Content: blocks, StopReason: StopReasonToolUse}
	}
	foreign := func(stop StopReason, blocks ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{API: APIOpenAIResponses, Provider: "other", Model: "other-1", Content: blocks, StopReason: stop}
	}
	image := ImageContent{MimeType: "image/png", Data: "iVBORw0KGgo="}
	later := SystemMessage{Content: SystemText("later"), Sections: OrderedSections{{Name: "s", Value: new("v")}}, ToolsAdded: []ToolSchema{
		{Name: "write", Description: "write a file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"}}, "required": []any{"path"}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: ConstrainedSamplingStrictPrefer}},
	}}
	if removal {
		later.ToolsRemoved = []ToolReference{{Name: "read"}}
		later.ToolsAdded = append(later.ToolsAdded, ToolSchema{Name: "read", Description: "read again", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}})
	}
	return []Message{
		SystemMessage{Content: SystemText("base prompt"), ToolsAdded: []ToolSchema{{Name: "read", Description: "read a file", Parameters: map[string]any{"type": "object"}}}},
		UserMessage{Content: UserText("start")},
		same(ThinkingContent{Thinking: "plan", ThinkingSignature: "sig"}, ThinkingContent{Thinking: "plain"}, ThinkingContent{Redacted: true, ThinkingSignature: "r"}, ThinkingContent{Thinking: "  "},
			TextContent{Text: "calling", TextSignature: "t"}, validityToolCall("a"), validityToolCall("b")),
		ToolResultMessage{ToolCallID: "a", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "A"}, image}},
		later,
		ToolResultMessage{ToolCallID: "b", ToolName: "read", IsError: true, Content: []ToolResultMessageContent{TextContent{Text: "B"}}},
		UserMessage{Content: UserContentBlocks{TextContent{Text: "look"}, image}},
		foreign(StopReasonToolUse, ThinkingContent{Thinking: "foreign thought", ThinkingSignature: "x"}, TextContent{Text: "hi", TextSignature: "z"}, ToolCall{ID: "call|with.odd/chars", Name: "read", Arguments: JsonObject{"path": "p", "": "empty key", "nested": map[string]any{"n": 1.5, "list": []any{"x", map[string]any{"": 2}}}}, ThoughtSignature: "ts"}),
		ToolResultMessage{ToolCallID: "call|with.odd/chars", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "C"}}},
		foreign(StopReasonError, TextContent{Text: "boom"}, validityToolCall("e")),
		ToolResultMessage{ToolCallID: "e", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "orphan"}}},
		same(validityToolCall("f"), validityToolCall("g"), ToolCall{ID: "|fc_stored", Name: "read", Arguments: JsonObject{"path": "q"}}),
		ToolResultMessage{ToolCallID: "|fc_stored", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "stored without a call id"}}},
		UserMessage{Content: UserText("interrupt")},
		ToolResultMessage{ToolCallID: "f", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "late"}}},
		foreign(StopReasonAborted, validityToolCall("h")),
		UserMessage{Content: UserText("next")},
	}
}

// borrowCanaryModels is one catalog model for every distinct request shape a provider converts for: its API, provider, image input, reasoning flag and compat flags (mid-conversation system messages and tool changes, additional tools, tool search, thinking formats...). A converter branch that only one compat combination reaches, such as passing a mid-conversation system message's tools straight to the payload, is then built at least once. The selection comes from the catalog, so a new compat flag or provider is covered without editing the test.
func borrowCanaryModels(t *testing.T) []*GeneratedModel {
	t.Helper()
	seen := map[string]bool{}
	var out []*GeneratedModel
	for i := range GeneratedModels {
		model := &GeneratedModels[i]
		switch model.API {
		case APIAnthropicMessages, APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses, APIGoogleVertex, APIGoogleGenerativeAI, APIMistralConversations, APIBedrockConverseStream, APIPiMessages:
		default:
			continue
		}
		compat, err := json.Marshal(model.Compat)
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("%s|%s|%v|%v|%s", model.API, model.Provider, slices.Contains(model.Capabilities, "image"), model.Reasoning, compat)
		if !seen[key] {
			seen[key] = true
			out = append(out, model)
		}
	}
	return out
}

// A request build borrows the transcript's messages instead of copying them (TranscriptContext.borrowed), so no API's conversion may change the transcript or anything it references. The transcript is compared with a deep copy taken before the build, for one catalog model of every request shape (borrowCanaryModels: every API, models that take images and models that do not, providers that take mid-conversation system messages and tool changes and providers that fold them), additive and non-additive tool changes, and a frozen transcript (which shares the caller's messages).
func TestRequestBuildLeavesTheTranscriptUnchanged(t *testing.T) {
	models := borrowCanaryModels(t)
	apis := map[API]bool{}
	midConversation := false
	for _, model := range models {
		apis[model.API] = true
		midConversation = midConversation || (model.Compat != nil && model.Compat.SupportsMidConvoSystemMessages != nil && *model.Compat.SupportsMidConvoSystemMessages)
	}
	if len(apis) < 10 || !midConversation {
		t.Fatalf("the catalog selection lost a request shape: %d APIs, mid-conversation system messages %v", len(apis), midConversation)
	}
	for _, model := range models {
		for _, variant := range []struct{ frozen, removal bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
			frozen := variant.frozen
			t.Run(fmt.Sprintf("%s/%s/frozen=%v/removal=%v", model.Provider, model.ID, frozen, variant.removal), func(t *testing.T) {
				var provider Provider
				if model.API == APIPiMessages {
					provider = NewPiMessagesProvider(PiMessagesConfig{BaseURL: "http://127.0.0.1:1", APIKey: "matrix-key", Model: model.ID, ProviderID: model.Provider})
				} else {
					provider = newMatrixProvider(t, model, "http://127.0.0.1:1", false)
				}
				t.Cleanup(func() { _ = provider.Close() })
				transcript := NormalizeContext(Context{Messages: richTranscriptMessages(model, variant.removal), Frozen: frozen})
				before := cloneMessages(transcript.messages)
				built := 0
				options := StreamOptions{APIKey: "matrix-key", Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}, OnPayload: func(any, *Model) (any, error) { built++; return nil, errors.New("stop") }}
				for range 2 {
					if stream, err := provider.Stream(invalidRequest(t), transcript, options); err == nil && stream != nil {
						_ = stream.Result()
					}
				}
				if built == 0 && model.API != APIBedrockConverseStream {
					t.Fatal("the request was never built")
				}
				if !reflect.DeepEqual(before, transcript.messages) {
					t.Fatalf("a request build changed the transcript (frozen=%v)", frozen)
				}
			})
		}
	}
}
