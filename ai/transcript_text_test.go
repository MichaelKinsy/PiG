package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// utils/text.ts contentText joins text blocks with a newline, including empty blocks.
func TestHarnessTextSystemBlockSeparator(t *testing.T) {
	for _, tc := range []struct {
		name   string
		blocks SystemTextBlocks
		want   string
	}{
		{"ordinary", SystemTextBlocks{{Text: "first"}, {Text: "second"}}, "first\nsecond"},
		{"empty", nil, ""},
		{"singleton", SystemTextBlocks{{Text: "one"}}, "one"},
		{"empty blocks", SystemTextBlocks{{}, {Text: "middle"}, {}}, "\nmiddle\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := SystemMessage{Content: tc.blocks}
			if got := RenderSystemMessageUpdate(message); got != tc.want {
				t.Fatalf("system block update = %q, want %q", got, tc.want)
			}
			if got := GetCurrentSystemPrompt([]Message{message}); got != tc.want {
				t.Fatalf("system prompt = %q, want %q", got, tc.want)
			}
		})
	}
}

// utils/text.ts getSystemMessageText filters empty strings, but not whitespace or empty named updates.
func TestHarnessTextEmptySystemSection(t *testing.T) {
	message := SystemMessage{Content: SystemText("base"), Sections: OrderedSections{
		{Name: "empty", Value: new("")}, {Name: "removed"}, {Name: "last", Value: new("last")},
	}}
	if got := GetCurrentSystemPrompt([]Message{message}); got != "base\n\nlast" {
		t.Fatalf("system prompt = %q, want %q", got, "base\n\nlast")
	}
	message.Content = SystemText("")
	message.Sections = OrderedSections{{Name: "empty", Value: new("")}, {Name: "space", Value: new(" \t")}}
	if got := GetCurrentSystemPrompt([]Message{message}); got != " \t" {
		t.Fatalf("whitespace prompt = %q", got)
	}
	message.Sections = message.Sections[:1]
	if got := GetCurrentSystemPrompt([]Message{message}); got != "" {
		t.Fatalf("empty prompt = %q", got)
	}
	if got := RenderSystemMessageUpdate(message); got != "Updated system prompt section \"empty\":\n\n" {
		t.Fatalf("empty update = %q", got)
	}
}

// utils/text.ts renderSystemMessageUpdate interpolates names literally, not as Go/JSON strings.
func TestHarnessTextSystemUpdateLiteralNames(t *testing.T) {
	message := SystemMessage{Content: SystemText(""), Sections: OrderedSections{
		{Name: "a\"b", Value: new("new")}, {Name: "x\ny"}, {Name: "slash\\\t日本", Value: new("")},
	}}
	want := "Updated system prompt section \"a\"b\":\n\nnew\n\nRemoved system prompt section \"x\ny\".\n\nUpdated system prompt section \"slash\\\t日本\":\n\n"
	if got := RenderSystemMessageUpdate(message); got != want {
		t.Fatalf("update = %q, want %q", got, want)
	}
}

// Both OpenAI converters call getSystemMessageText for the leading instruction and
// renderSystemMessageUpdate for later instructions when the model supports them.
func TestSystemTextProductionRequests(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		for _, providerID := range []string{"openai", "github-copilot"} {
			for _, mid := range []bool{false, true} {
				t.Run(string(api)+"/"+providerID+"/mid="+map[bool]string{false: "false", true: "true"}[mid], func(t *testing.T) {
					messages := []Message{
						SystemMessage{Content: SystemTextBlocks{{Text: "first"}, {Text: "second"}}, Sections: OrderedSections{
							{Name: "empty", Value: new("")}, {Name: "removed"}, {Name: "last", Value: new("last")},
						}},
						UserMessage{Content: UserText("hello")},
						SystemMessage{Content: SystemTextBlocks{{Text: "third"}, {Text: "fourth"}}, Sections: OrderedSections{
							{Name: "a\"b", Value: new("new")}, {Name: "x\ny"}, {Name: "last", Value: new("")},
						}},
					}
					before, err := json.Marshal(messages)
					if err != nil {
						t.Fatal(err)
					}
					requests := make(chan map[string]json.RawMessage, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						requests <- body
						w.Header().Set("Content-Type", "text/event-stream")
						reply := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
						if api == APIOpenAIResponses {
							reply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
						}
						_, _ = io.WriteString(w, reply)
					}))
					defer server.Close()
					var provider Provider
					key := "messages"
					if api == APIOpenAICompletions {
						provider = NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "test", ProviderID: providerID, Compat: &OpenAICompat{SupportsMidConvoSystemMessages: new(mid)}})
					} else {
						key = "input"
						provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: "test", ProviderID: providerID, Compat: &ModelCompat{SupportsMidConvoSystemMessages: new(mid), SupportsDeveloperRole: new(false)}})
					}
					defer func() {
						if err := provider.Close(); err != nil {
							t.Error(err)
						}
					}()
					stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), StreamOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if result := stream.Result(); result == nil || result.StopReason != StopReasonStop {
						t.Fatalf("result = %#v", result)
					}
					var got []map[string]any
					select {
					case body := <-requests:
						if err := json.Unmarshal(body[key], &got); err != nil {
							t.Fatal(err)
						}
					default:
						t.Fatal("no provider request")
					}
					var userContent any = "hello"
					if api == APIOpenAIResponses {
						userContent = []any{map[string]any{"type": "input_text", "text": "hello"}}
					}
					prompt := "first\nsecond\n\nthird\nfourth\n\nnew"
					if mid {
						prompt = "first\nsecond\n\nlast"
					}
					want := []map[string]any{{"role": "system", "content": prompt}, {"role": "user", "content": userContent}}
					if mid {
						want = append(want, map[string]any{"role": "system", "content": "third\nfourth\n\nUpdated system prompt section \"a\"b\":\n\nnew\n\nRemoved system prompt section \"x\ny\".\n\nUpdated system prompt section \"last\":\n\n"})
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s = %#v, want %#v", key, got, want)
					}
					after, err := json.Marshal(messages)
					if err != nil {
						t.Fatal(err)
					}
					if string(after) != string(before) {
						t.Fatal("request mutated original messages")
					}
				})
			}
		}
	}
}

func BenchmarkSystemTextTranscript(b *testing.B) {
	blocks := make(SystemTextBlocks, 256)
	for i := range blocks {
		blocks[i].Text = strings.Repeat("instruction ", 32)
	}
	messages := []Message{SystemMessage{Content: blocks, Sections: OrderedSections{{Name: "empty", Value: new("")}, {Name: "rules", Value: new("rules")}}}}
	b.ReportAllocs()
	for b.Loop() {
		_ = GetCurrentSystemPrompt(messages)
	}
}
