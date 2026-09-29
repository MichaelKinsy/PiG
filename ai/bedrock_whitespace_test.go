package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestReviewProvidersBedrockWhitespace(t *testing.T) {
	// Pi bedrock-converse-stream.ts:912-918,974-1033,1090 uses ECMAScript trim only to test emptiness. Nonblank payloads retain their original bytes.
	for _, tc := range []struct {
		name, text string
		blank      bool
	}{
		{"empty", "", true},
		{"ASCII", " \t\r\n", true},
		{"BOM", "\ufeff", true},
		{"NBSP", "\u00a0", true},
		{"NEL", "\u0085", false},
		{"ordinary", " text ", false},
		{"surrounded NEL", " \u0085 ", false},
		{"BOM stress", strings.Repeat("\ufeff", 1<<15), true},
		{"NEL stress", strings.Repeat("\u0085", 1<<15), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.text
			if tc.blank {
				want = "<empty>"
			}
			for _, content := range []UserContent{UserText(tc.text), UserContentBlocks{TextContent{Text: tc.text}}} {
				messages, err := convertBedrockMessages([]Message{UserMessage{Content: content}}, "amazon.nova-lite-v1:0", "", bedrockCacheNone, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(messages) != 1 || len(messages[0].Content) != 1 {
					t.Fatalf("user %T changed turn/block count: %#v", content, messages)
				}
				text, ok := messages[0].Content[0].(*btypes.ContentBlockMemberText)
				if !ok || text.Value != want {
					t.Errorf("user %T: %#v, want %q", content, messages[0].Content, want)
				}
			}
			result, err := bedrockToolResultBlock(ToolResultMessage{ToolCallID: "call", Content: []ToolResultMessageContent{TextContent{Text: tc.text}}})
			if err != nil {
				t.Fatal(err)
			}
			tool := result.(*btypes.ContentBlockMemberToolResult).Value
			if text, ok := tool.Content[0].(*btypes.ToolResultContentBlockMemberText); !ok || text.Value != want {
				t.Errorf("tool result=%#v, want %q", tool.Content, want)
			}
			for _, model := range []string{"anthropic.claude-sonnet-4-5-20250929-v1:0", "amazon.nova-lite-v1:0"} {
				for _, block := range []AssistantContentBlock{TextContent{Text: tc.text}, ThinkingContent{Thinking: tc.text, ThinkingSignature: "sig"}} {
					content, err := bedrockAssistantContent([]AssistantContentBlock{block}, model, "")
					if err != nil {
						t.Fatal(err)
					}
					if (len(content) == 0) != tc.blank {
						t.Errorf("%s %T emptiness=%#v, want blank=%v", model, block, content, tc.blank)
					}
				}
				content, err := bedrockAssistantContent([]AssistantContentBlock{ThinkingContent{Thinking: " reason ", ThinkingSignature: tc.text}}, model, "")
				if err != nil || len(content) != 1 {
					t.Fatalf("signature %s: content=%v err=%v", model, content, err)
				}
				if model == "anthropic.claude-sonnet-4-5-20250929-v1:0" && tc.blank {
					if text, ok := content[0].(*btypes.ContentBlockMemberText); !ok || text.Value != " reason " {
						t.Errorf("blank signature did not fall back to text: %#v", content[0])
					}
				} else {
					reasoning, ok := content[0].(*btypes.ContentBlockMemberReasoningContent)
					if !ok {
						t.Errorf("nonblank signature lost reasoning: %#v", content[0])
						continue
					}
					text := reasoning.Value.(*btypes.ReasoningContentBlockMemberReasoningText).Value
					wantSignature := tc.text
					if model == "amazon.nova-lite-v1:0" {
						wantSignature = ""
					}
					if aws.ToString(text.Text) != " reason " || aws.ToString(text.Signature) != wantSignature {
						t.Errorf("reasoning=%#v, want unchanged text/signature %q", text, wantSignature)
					}
				}
			}
		})
	}
}

func TestBedrockSanitizesBeforeCheckingAssistantEmptiness(t *testing.T) {
	// bedrock-converse-stream.ts:913-914,1022-1023 sanitizes before testing trim length. These WTF-8 inputs represent an unpaired U+D800, alone and followed by BOM whitespace.
	for _, text := range []string{"\xed\xa0\x80", "\xed\xa0\x80\ufeff"} {
		for _, block := range []AssistantContentBlock{TextContent{Text: text}, ThinkingContent{Thinking: text, ThinkingSignature: "sig"}} {
			content, err := bedrockAssistantContent([]AssistantContentBlock{block}, "anthropic.claude-sonnet-4-5-20250929-v1:0", "")
			if err != nil || len(content) != 0 {
				t.Errorf("sanitized %T content=%#v err=%v, want no blank block", block, content, err)
			}
		}
	}
}

func TestBedrockSurrogateEmptinessUpstream(t *testing.T) {
	model := cloneGeneratedModel(t, "amazon-bedrock/us.anthropic.claude-sonnet-4-5-20250929-v1:0").ToModel()
	const unpairedHighSurrogate = "\xed\xa0\xbd" // WTF-8 for the exact U+D83D input in upstream cases 272 and 282.
	for _, tc := range []struct {
		name         string
		message      Message
		wantMessages int
	}{
		{"replaces user content emptied by surrogate sanitization with a placeholder", UserMessage{Content: UserText(unpairedHighSurrogate)}, 1},
		{"skips assistant text blocks emptied by surrogate sanitization", AssistantMessage{API: APIBedrockConverseStream, Provider: "amazon-bedrock", Model: model.ID, StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: unpairedHighSurrogate}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := captureBedrockCommand(t, model, Context{Messages: []Message{tc.message}}, StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"PI_CACHE_RETENTION": "none"}})
			if len(input.Messages) != tc.wantMessages {
				t.Fatalf("messages=%#v, want %d", input.Messages, tc.wantMessages)
			}
			if tc.wantMessages == 1 {
				if len(input.Messages[0].Content) != 1 {
					t.Fatalf("content=%#v", input.Messages[0].Content)
				}
				if text, ok := input.Messages[0].Content[0].(*btypes.ContentBlockMemberText); !ok || text.Value != "<empty>" {
					t.Fatalf("content=%#v, want <empty>", input.Messages[0].Content)
				}
			}
		})
	}
}

func TestBedrockFoldsMidConversationSystemMessagesUpstream(t *testing.T) {
	// Pi bedrock-converse-stream.ts:131-132 always applies collapseSystemMessages (transcript.ts:73-111), and BedrockCompat (types.ts:843-846) has no supportsMidConvoSystemMessages. Later system text therefore joins the leading prompt with "\n\n" whatever the model compat claims; it never becomes a user turn, and whitespace never decides whether it survives.
	for _, midConversation := range []bool{false, true} {
		for _, update := range []string{"later instructions", "\ufeff", "\u0085", " \t"} {
			t.Run(fmt.Sprintf("compat=%t/%q", midConversation, update), func(t *testing.T) {
				model := cloneGeneratedModel(t, "amazon-bedrock/us.anthropic.claude-sonnet-4-5-20250929-v1:0").ToModel()
				model.ProviderMeta.Compat = &ModelCompat{SupportsMidConvoSystemMessages: new(midConversation)}
				input := captureBedrockCommand(t, model, Context{Messages: []Message{
					SystemMessage{Content: SystemText("base")},
					UserMessage{Content: UserText("one")},
					SystemMessage{Content: SystemText(update)},
					UserMessage{Content: UserText("two")},
				}}, StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{"PI_CACHE_RETENTION": "none"}})
				if len(input.System) != 1 {
					t.Fatalf("system=%#v, want one text block", input.System)
				}
				if system, ok := input.System[0].(*btypes.SystemContentBlockMemberText); !ok || system.Value != "base\n\n"+update {
					t.Errorf("system=%#v, want %q", input.System[0], "base\n\n"+update)
				}
				var turns []string
				for _, message := range input.Messages {
					for _, block := range message.Content {
						if text, ok := block.(*btypes.ContentBlockMemberText); ok {
							turns = append(turns, string(message.Role)+":"+text.Value)
						}
					}
				}
				if want := []string{"user:one", "user:two"}; !reflect.DeepEqual(turns, want) {
					t.Errorf("messages=%q, want %q", turns, want)
				}
			})
		}
	}
}

func BenchmarkBedrockWhitespaceConversion(b *testing.B) {
	messages := []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: strings.Repeat("\ufeff", 2048)}, TextContent{Text: strings.Repeat("\u0085", 2048)}}},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ordinary response"}, ThinkingContent{Thinking: "reasoning", ThinkingSignature: "\ufeff"}, ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "call", Content: []ToolResultMessageContent{TextContent{Text: "\ufeff"}, TextContent{Text: "result"}}},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := convertBedrockMessages(messages, "anthropic.claude-sonnet-4-5-20250929-v1:0", "", bedrockCacheNone, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBedrockWhitespaceConverseWire(t *testing.T) {
	// Drive NormalizeContext -> BedrockProvider.Stream -> the real AWS serializer and a hermetic HTTP endpoint. The poisoned signature is a persisted-history shape explicitly handled at bedrock-converse-stream.ts:1029-1036.
	for _, key := range []string{"HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME"} {
		t.Setenv(key, t.TempDir())
	}
	for _, key := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_BEARER_TOKEN_BEDROCK"} {
		t.Setenv(key, "")
	}
	for _, model := range []string{"anthropic.claude-sonnet-4-5-20250929-v1:0", "amazon.nova-lite-v1:0"} {
		for _, tc := range []struct{ name, value, want string }{{"BOM", "\ufeff", "<empty>"}, {"NEL", "\u0085", "\u0085"}} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				requests := make(chan map[string]any, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					requests <- body
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"message":"captured request"}`)
				}))
				defer server.Close()
				provider := NewBedrockProvider(model, server.URL)
				defer func() { _ = provider.Close() }()
				request := Context{Messages: []Message{
					UserMessage{Content: UserText(tc.value)},
					AssistantMessage{API: APIBedrockConverseStream, Provider: "amazon-bedrock", Model: model, StopReason: StopReasonStop, Content: []AssistantContentBlock{
						TextContent{Text: tc.value}, ThinkingContent{Thinking: "reason", ThinkingSignature: tc.value}, ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{}},
					}},
					ToolResultMessage{ToolCallID: "call", ToolName: "tool", Content: []ToolResultMessageContent{TextContent{Text: tc.value}}},
				}}
				stream, err := provider.Stream(t.Context(), NormalizeContext(request), StreamOptions{CacheRetention: CacheRetentionNone, Env: ProviderEnv{
					"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1", "PI_CACHE_RETENTION": "none", "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": "*",
				}})
				failed := err != nil
				if stream != nil {
					failed = stream.Result().StopReason == StopReasonError
				}
				if !failed {
					t.Fatal("expected hermetic HTTP failure after request capture")
				}
				var body map[string]any
				select {
				case body = <-requests:
				default:
					t.Fatalf("request never reached serializer/transport: %v", err)
				}
				assistant := []any{}
				if tc.name == "NEL" {
					assistant = append(assistant, map[string]any{"text": tc.value})
				}
				if strings.HasPrefix(model, "anthropic.") && tc.name == "BOM" {
					assistant = append(assistant, map[string]any{"text": "reason"})
				} else {
					reason := map[string]any{"text": "reason"}
					if strings.HasPrefix(model, "anthropic.") {
						reason["signature"] = tc.value
					}
					assistant = append(assistant, map[string]any{"reasoningContent": map[string]any{"reasoningText": reason}})
				}
				assistant = append(assistant, map[string]any{"toolUse": map[string]any{"toolUseId": "call", "name": "tool", "input": map[string]any{}}})
				want := []any{
					map[string]any{"role": "user", "content": []any{map[string]any{"text": tc.want}}},
					map[string]any{"role": "assistant", "content": assistant},
					map[string]any{"role": "user", "content": []any{map[string]any{"toolResult": map[string]any{"toolUseId": "call", "content": []any{map[string]any{"text": tc.want}}, "status": "success"}}}},
				}
				if !reflect.DeepEqual(body["messages"], want) {
					gotJSON, _ := json.Marshal(body["messages"])
					wantJSON, _ := json.Marshal(want)
					t.Fatalf("wire messages=%s, want %s", gotJSON, wantJSON)
				}
			})
		}
	}
}
