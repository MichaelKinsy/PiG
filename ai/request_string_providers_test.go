//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// documentStrings returns every string value of a decoded JSON document.
func documentStrings(value any, into map[string]bool) {
	switch value := value.(type) {
	case string:
		into[value] = true
	case []any:
		for _, element := range value {
			documentStrings(element, into)
		}
	case map[string]any:
		for _, element := range value {
			documentStrings(element, into)
		}
	}
}

// Every provider re-sends the whole conversation, so a long conversation's request must not encode each earlier tool result's text again (see TestOpenAICompletionsEncodesEachLargeToolResultOnce). The body is JSON.stringify's: '<', '>' and '&' as they are.
func TestProvidersEncodeEachLargeToolResultOnce(t *testing.T) {
	for _, shape := range streamFrameShapes {
		if strings.HasSuffix(shape.name, "toolcall") {
			continue
		}
		for _, hook := range []string{"no hook", "hook returns the payload"} {
			t.Run(shape.name+"/"+hook, func(t *testing.T) {
				testProviderEncodesEachLargeToolResultOnce(t, shape, hook != "no hook")
			})
		}
	}
}

// A coding session installs a payload hook that returns the payload unchanged when no extension handles the event, so the spliced path must serve that request too.
func testProviderEncodesEachLargeToolResultOnce(t *testing.T, shape streamFrameShape, hooked bool) {
	const results = 4
	var bodies [][]byte
	response := shape.body(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") == "zstd" {
			// The Codex SSE body is compressed as Pi's compressRequestBodyZstd does; the encoding is a property of the JSON inside it.
			var err error
			if body, err = decodeZstdFrameForTest(body); err != nil {
				t.Error(err)
			}
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	provider := shape.provider(server.URL)
	defer func() { _ = provider.Close() }()
	var messages []Message
	var texts []string
	for index := range results {
		text := strings.Repeat("unique "+shape.name+fmt.Sprint(hooked)+uniqueRun()+" <"+strings.Repeat(string(rune('a'+index)), 5)+"> \"line\"\n", 400) + "end"
		texts = append(texts, text)
		id := string(rune('a' + index))
		messages = append(messages,
			UserMessage{Content: UserText("read " + id)},
			AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": id}}}},
			ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: text}}})
	}
	messages = append(messages, UserMessage{Content: UserText("next")})
	send := func() {
		options := StreamOptions{}
		if hooked {
			options.OnPayload = func(payload any, _ *Model) (any, error) { return payload, nil }
		}
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages, Frozen: true}), options)
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(context.WithoutCancel(t.Context())) {
		}
	}
	before := requestStrings.misses
	send()
	send()
	if len(bodies) != 2 {
		t.Fatalf("%d requests", len(bodies))
	}
	if got := requestStrings.misses - before; got != results {
		t.Fatalf("the %d large results were encoded %d times over two requests", results, got)
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("the same conversation produced different bodies")
	}
	if bytes.Contains(bodies[1], []byte(`\u003c`)) || !bytes.Contains(bodies[1], []byte(" <aaaaa> ")) {
		t.Fatalf("a large tool result was not written as JSON.stringify writes it: %.200q", bodies[1])
	}
	var decoded any
	if err := json.Unmarshal(bodies[1], &decoded); err != nil {
		t.Fatal(err)
	}
	carried := map[string]bool{}
	documentStrings(decoded, carried)
	for index, text := range texts {
		if !carried[text] {
			t.Errorf("the request does not carry tool result %d's text", index)
		}
	}
}

// The body a provider sends with the cache is the body it sends without one, for text that JSON escapes in every way and for large strings in every message role. The cache is the process-wide one, so the test runs alone and leaves it as it found it.
func TestProvidersSendTheSameBodyWithAndWithoutTheCache(t *testing.T) {
	atoms := []string{"func main() {\n\tfmt.Println(\"<hi> & bye\")\n}\n", "\u2028\u2029", "\x00\x01\x1f\x7f", "😀 café 漢字", "\\\"", "</script>&amp;"}
	random := rand.New(rand.NewPCG(7, 8))
	large := func() string {
		var builder strings.Builder
		for builder.Len() < 3*requestSpliceMinBytes {
			builder.WriteString(atoms[random.IntN(len(atoms))])
		}
		return builder.String() + "end"
	}
	var messages []Message
	for index := range 5 {
		id := string(rune('a' + index))
		messages = append(messages,
			UserMessage{Content: UserText(large())},
			AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
				TextContent{Text: large()}, ToolCall{ID: id, Name: "write", Arguments: JsonObject{"path": id, "content": large()}}}},
			ToolResultMessage{ToolCallID: id, ToolName: "write", Content: []ToolResultMessageContent{TextContent{Text: large()}}},
			AssistantMessage{API: "openai-completions", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: id + "2", Name: "read", Arguments: JsonObject{"path": "a"}}}},
			ToolResultMessage{ToolCallID: id + "2", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "short"}}})
	}
	messages = append(messages, UserMessage{Content: UserText("next")})
	saved := requestStrings
	defer func() { requestStrings = saved }()
	for _, shape := range streamFrameShapes {
		if strings.HasSuffix(shape.name, "toolcall") {
			continue
		}
		for _, hooked := range []bool{false, true} {
			var body []byte
			response := shape.body(1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, response)
			}))
			send := func() []byte {
				provider := shape.provider(server.URL)
				options := StreamOptions{}
				if hooked {
					options.OnPayload = func(payload any, _ *Model) (any, error) { return payload, nil }
				}
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages, Frozen: true}), options)
				if err != nil {
					t.Fatal(err)
				}
				for range stream.Events(context.WithoutCancel(t.Context())) {
				}
				_ = provider.Close()
				return body
			}
			requestStrings = nil
			plain := bytes.Clone(send())
			requestStrings = newQuotedStringCache(1 << 30)
			for pass := range 2 {
				if spliced := send(); !bytes.Equal(spliced, plain) {
					at := 0
					for at < len(plain) && at < len(spliced) && plain[at] == spliced[at] {
						at++
					}
					t.Errorf("%s hooked=%v pass %d: the body differs from the uncached body at byte %d of %d/%d\n spliced %.100q\n   plain %.100q", shape.name, hooked, pass, at, len(spliced), len(plain), spliced[max(at-20, 0):], plain[max(at-20, 0):])
				}
			}
			if requestStrings.misses == 0 {
				t.Errorf("%s hooked=%v: the cache was not used", shape.name, hooked)
			}
			server.Close()
		}
	}
}

// A payload hook that replaces the request's input is sent as it replaced it: the spliced body is built only from the input the provider made.
func TestOpenAIResponsesSendsAHookReplacedInputAsReplaced(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streamFrameShapes[2].body(1))
	}))
	defer server.Close()
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "k", Model: "gpt-bench", ProviderID: "openai"})
	defer func() { _ = provider.Close() }()
	text := strings.Repeat("a replaced-input tool result <\n", 600) + "end"
	messages := []Message{
		UserMessage{Content: UserText("read")},
		AssistantMessage{API: "openai-responses", Provider: "openai", Model: "gpt-bench", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "c|i", Name: "read", Arguments: JsonObject{"path": "a"}}}},
		ToolResultMessage{ToolCallID: "c|i", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: text}}},
	}
	options := StreamOptions{OnPayload: func(payload any, _ *Model) (any, error) {
		request := payload.(respRequest)
		request.Input = json.RawMessage(`[{"role":"user","content":"replaced"}]`)
		return request, nil
	}}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages, Frozen: true}), options)
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(context.WithoutCancel(t.Context())) {
	}
	var sent struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &sent); err != nil || len(sent.Input) != 1 || sent.Input[0]["content"] != "replaced" {
		t.Fatalf("the hook's input was not sent: %v %.200q", err, body)
	}
}

var providerTestRuns atomic.Int64

// uniqueRun makes a test's text new to the process-wide cache each time the test runs, so -count=N counts the same encodings.
func uniqueRun() string { return fmt.Sprint(providerTestRuns.Add(1)) }

// The cached body equals the uncached body for every large string a request carries besides message text: the system prompt, a tool's description and parameter schema, thinking text and its signature from the same model, image data, nested tool-call arguments, and an error tool result. A hook that returns a replacement object is sent as it returned it.
func TestProvidersSendTheSameBodyForEveryLargeStringOfARequest(t *testing.T) {
	atoms := []string{"a<b>&c", "\u2028\u2029", "\x00\x1f\x7f", "😀 café", "\\\"", "</script>", "\t\r\n", "\ufeff", "e\u0301"}
	random := rand.New(rand.NewPCG(9, 10))
	large := func() string {
		var builder strings.Builder
		for builder.Len() < 2*requestSpliceMinBytes {
			builder.WriteString(atoms[random.IntN(len(atoms))])
		}
		return builder.String()
	}
	image := strings.Repeat("iVBORw0KGgo", 1000)
	// The assistant turns come from the model the request goes to, so its thinking and signatures are sent as they are.
	conversation := func(api API, provider, model string) []Message {
		var messages []Message
		for index := range 3 {
			// Nine letters: Mistral's own tool call ids.
			id := strings.Repeat(string(rune('a'+index)), 9)
			messages = append(messages,
				UserMessage{Content: UserContentBlocks{TextContent{Text: large()}, ImageContent{Data: image, MimeType: "image/png"}}},
				AssistantMessage{API: api, Provider: provider, Model: model, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
					ThinkingContent{Thinking: large(), ThinkingSignature: strings.Repeat("sig", 2000)},
					TextContent{Text: large()},
					ToolCall{ID: id, Name: "write", Arguments: JsonObject{"path": id, "nested": map[string]any{"deep": []any{large(), 1.0, nil}}, "content": large()}}}},
				ToolResultMessage{ToolCallID: id, ToolName: "write", Content: []ToolResultMessageContent{TextContent{Text: large()}, ImageContent{Data: image, MimeType: "image/png"}}, IsError: index == 1})
		}
		return append(messages, UserMessage{Content: UserText(large())})
	}
	models := map[string][3]string{
		"openai-completions":     {string(APIOpenAICompletions), "openai", "gpt-bench"},
		"openai-responses":       {string(APIOpenAIResponses), "openai", "gpt-bench"},
		"openai-codex-responses": {string(APIOpenAICodexResponses), "openai-codex", "gpt-bench"},
		"anthropic":              {string(APIAnthropicMessages), "anthropic", "claude-bench"},
		"google":                 {string(APIGoogleGenerativeAI), "google", "gemini-2.5-flash"},
		"mistral":                {string(APIMistralConversations), "mistral", "mistral-bench"},
	}
	tools := []ToolSchema{{Name: "write", Description: large(), Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": large()}}}}}
	system := large()
	replacement := large()
	saved := requestStrings
	defer func() { requestStrings = saved }()
	for _, shape := range streamFrameShapes {
		if strings.HasSuffix(shape.name, "toolcall") {
			continue
		}
		model, ok := models[strings.TrimSuffix(shape.name, "-text")]
		if !ok {
			t.Fatalf("no model for %s", shape.name)
		}
		messages := conversation(API(model[0]), model[1], model[2])
		for _, hook := range []string{"none", "returns the payload", "returns a replacement object"} {
			// Google and Mistral read the payload's fields, so a replacement object of another shape is an error there.
			if hook == "returns a replacement object" && (strings.HasPrefix(shape.name, "google") || strings.HasPrefix(shape.name, "mistral")) {
				continue
			}
			var body []byte
			response := shape.body(1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, response)
			}))
			send := func() []byte {
				provider := shape.provider(server.URL)
				options := StreamOptions{}
				switch hook {
				case "returns the payload":
					options.OnPayload = func(payload any, _ *Model) (any, error) { return payload, nil }
				case "returns a replacement object":
					options.OnPayload = func(payload any, _ *Model) (any, error) {
						return map[string]any{"wrapped": payload, "model": "gpt-bench", "messages": []any{map[string]any{"role": "user", "content": replacement}}}, nil
					}
				}
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: system, Tools: tools, Messages: messages, Frozen: true}), options)
				if err != nil {
					t.Fatal(err)
				}
				for range stream.Events(context.WithoutCancel(t.Context())) {
				}
				_ = provider.Close()
				return body
			}
			requestStrings = nil
			plain := bytes.Clone(send())
			requestStrings = newQuotedStringCache(1 << 30)
			for pass := range 2 {
				if spliced := send(); !bytes.Equal(spliced, plain) {
					at := 0
					for at < len(plain) && at < len(spliced) && plain[at] == spliced[at] {
						at++
					}
					t.Errorf("%s hook %s pass %d: the body differs from the uncached body at byte %d of %d/%d\n spliced %.100q\n   plain %.100q", shape.name, hook, pass, at, len(spliced), len(plain), spliced[max(at-20, 0):], plain[max(at-20, 0):])
				}
			}
			if hook != "returns a replacement object" && requestStrings.misses == 0 {
				t.Errorf("%s hook %s: the cache was not used", shape.name, hook)
			}
			server.Close()
		}
	}
}
