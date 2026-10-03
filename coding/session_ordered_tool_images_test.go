package coding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

type orderedImageTool struct {
	fakeTool
	content []ai.ToolResultMessageContent
}

func (tool *orderedImageTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: tool.content, Details: map[string]any{"marker": "retained"}}, nil
}

// Pi agent-session.ts:558-578 awaits image normalization after tool_result hooks, then agent-loop.ts:880-894 carries the same ordered blocks into messages. Empty text and [] remain distinct on disk and after reopen.
func TestSessionOrderedToolResultImagesSurviveHooksEventsProviderAndReopen(t *testing.T) {
	bmp := ai.ImageContent{Data: "Qk06AAAAAAAAADYAAAAoAAAAAQAAAAEAAAABABgAAAAAAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAD/AA==", MimeType: "image/bmp"}
	for _, tc := range []struct {
		name    string
		content []ai.ToolResultMessageContent
	}{
		{"empty", []ai.ToolResultMessageContent{}},
		{"empty text", []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}},
		{"interleaved", []ai.ToolResultMessageContent{ai.TextContent{Text: "before", TextSignature: "signed-tool-text"}, bmp, ai.TextContent{Text: ""}, ai.ImageContent{Data: "bm90LWFuLWltYWdl", MimeType: "image/png"}, ai.TextContent{Text: "after"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.content)
			hookCalled := false
			var providerContent []ai.ToolResultMessageContent
			h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{&orderedImageTool{fakeTool: fakeTool{name: "ordered"}, content: tc.content}}, extension: extension.Extension{Path: "ordered", Handlers: map[string][]extension.HandlerFn{"tool_result": {func(args ...any) (any, error) {
				event := args[0].(extension.CustomToolResultEvent)
				data, err := json.Marshal(event.Content)
				if err != nil {
					t.Fatal(err)
				}
				expected, err := json.Marshal(tc.content)
				if err != nil {
					t.Fatal(err)
				}
				var gotJSON, wantJSON any
				if err := json.Unmarshal(data, &gotJSON); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(expected, &wantJSON); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotJSON, wantJSON) {
					t.Errorf("hook content=%s, want %s", data, expected)
				}
				hookCalled = true
				return &extension.ToolResultEventResult{Content: event.Content}, nil
			}}}}}, fauxToolCall("ordered"), func(messages []ai.Message) *ai.AssistantMessage {
				for _, message := range messages {
					if result, ok := message.(ai.ToolResultMessage); ok {
						providerContent = result.Content
					}
				}
				return fauxReply("done", ai.StopReasonStop, 0)(messages)
			})
			if _, err := h.session.Send(t.Context(), "images"); err != nil {
				t.Fatal(err)
			}
			if err := h.session.Close(); err != nil {
				t.Fatal(err)
			}
			<-h.done
			if !hookCalled {
				t.Fatal("tool_result hook not called")
			}
			if !reflect.DeepEqual(original, tc.content) {
				t.Fatal("normalization mutated the tool's content")
			}
			want := tc.content
			if tc.name == "interleaved" {
				if len(providerContent) != len(tc.content)+1 {
					t.Fatalf("provider content=%#v", providerContent)
				}
				converted, ok := providerContent[1].(ai.ImageContent)
				if !ok || converted.MimeType != "image/png" {
					t.Fatalf("converted image=%#v", providerContent[1])
				}
				data, err := base64.StdEncoding.DecodeString(converted.Data)
				if err != nil {
					t.Fatal(err)
				}
				picture, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				r, g, b, a := picture.At(0, 0).RGBA()
				if picture.Bounds().Dx() != 1 || picture.Bounds().Dy() != 1 || r != 65535 || g != 0 || b != 0 || a != 65535 {
					t.Fatal("converted BMP lost original red pixel")
				}
				want = append([]ai.ToolResultMessageContent{tc.content[0], converted, ai.TextContent{Text: "[Image converted from image/bmp to image/png.]"}}, tc.content[2:]...)
			}
			if !reflect.DeepEqual(providerContent, want) {
				t.Fatalf("provider content=%#v, want %#v", providerContent, want)
			}
			seen := false
			for _, event := range h.events {
				if end, ok := event.(agent.ToolExecutionEndEvent); ok {
					seen = true
					if !reflect.DeepEqual(end.Result.Content, want) {
						t.Fatalf("execution end=%#v, want %#v", end.Result.Content, want)
					}
				}
			}
			if !seen {
				t.Fatal("execution end missing")
			}
			reopened, err := NewSession(h.session.services, SessionOptions{Model: h.session.agent.Model(), ResumePath: h.session.Path(), SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			seen = false
			for _, message := range reopened.agent.Messages() {
				if message.ToolResult != nil {
					seen = true
					if !reflect.DeepEqual(message.ToolResult.Content, want) {
						t.Fatalf("reopened content=%#v, want %#v", message.ToolResult.Content, want)
					}
					if details, _ := orderedjson.Map(message.ToolResult.Details); !reflect.DeepEqual(details, map[string]any{"marker": "retained"}) {
						t.Fatalf("details=%#v", message.ToolResult.Details)
					}
				}
			}
			if !seen {
				t.Fatal("reopened history lost tool result")
			}
		})
	}
}
