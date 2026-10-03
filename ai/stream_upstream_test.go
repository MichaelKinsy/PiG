package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gorilla/websocket"
)

type upstreamStreamCase struct {
	Source, Name, Kind, Provider, Model string
	API                                 API
	HasCompat                           bool
	Options                             json.RawMessage
	CustomModel                         json.RawMessage
	Request                             json.RawMessage
}

// Every row carries its exact .upstream/v0.99.1/packages/ai/test/stream.test.ts source location. The extractor evaluates only wrapper inputs; all behavior assertions below are Go ports of the six upstream helpers.
func TestStreamUpstream(t *testing.T) {
	data, err := os.ReadFile("testdata/stream-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	current, err := exec.CommandContext(t.Context(), "node", "../test/parity/testdata/extract-stream-cases.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("extract upstream cases: %v\n%s", err, current)
	}
	if !bytes.Equal(data, current) {
		t.Fatal("stream case inputs drifted; regenerate with node test/parity/testdata/extract-stream-cases.mjs > ai/testdata/stream-cases.json")
	}
	var cases []upstreamStreamCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) { runUpstreamStreamCase(t, tc) })
	}
}

func runUpstreamStreamCase(t *testing.T, tc upstreamStreamCase) {
	t.Helper()
	t.Log(tc.Source)
	var model GeneratedModel
	if tc.Provider == "ollama" {
		model = GeneratedModel{Provider: "ollama", ID: "gpt-oss:20b", DisplayName: "Ollama GPT-OSS 20B", API: APIOpenAICompletions, Reasoning: true, Capabilities: []string{"text"}, ContextWindow: 128000, MaxOutputTokens: 16000}
	} else {
		g, ok := LookupModelExact(tc.Provider + "/" + tc.Model)
		if !ok {
			t.Fatal("missing upstream model")
		}
		model = *g
	}
	model.API = tc.API
	if !tc.HasCompat {
		model.Compat = nil
	}
	var options StreamOptions
	if err := json.Unmarshal(tc.Options, &options); err != nil {
		t.Fatal(err)
	}
	options.IsReasoning = model.Reasoning
	options.ModelCost = model.ToModel().CostRates()
	options.Env = ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}
	var rawOptions map[string]any
	if err := json.Unmarshal(tc.Options, &rawOptions); err != nil {
		t.Fatal(err)
	}
	if tc.Kind == "handleImage" && !slices.Contains(model.Capabilities, "image") {
		t.Log("upstream helper returns for a model without image input")
		return
	}
	var image string
	if tc.Kind == "handleImage" {
		data, err := os.ReadFile("../.upstream/current/packages/ai/test/data/red-circle.png")
		if err != nil {
			t.Fatal(err)
		}
		image = base64.StdEncoding.EncodeToString(data)
	}
	var calls atomic.Int32
	reply := func(w http.ResponseWriter, body any) {
		call := int(calls.Add(1))
		validateStreamNativeOptions(t, model, rawOptions, body, tc)
		switch tc.Kind {
		case "basicTextGeneration":
			text := "Hello test successful"
			if call == 2 {
				text = "Goodbye test successful"
			}
			writeMatrixUsageResponse(t, w, model.API, text, call == 2)
		case "handleToolCall":
			writeMatrixToolCall(t, w, model.API, "math00001", "math_operation", JsonObject{"a": 15, "b": 27, "operation": "add"})
		case "handleStreaming":
			writeMatrixUsageResponse(t, w, model.API, "1, 2, 3", false)
		case "handleThinking":
			writeMatrixUsageResponse(t, w, model.API, "44", false, "17 plus 27 is 44")
		case "handleImage":
			if !jsonContainsText(body, image) {
				t.Error("image did not reach provider")
			}
			writeMatrixUsageResponse(t, w, model.API, "A red circle.", false)
		case "multiTurn":
			if call == 1 {
				writeMatrixToolCall(t, w, model.API, "math00001", "math_operation", JsonObject{"a": 42, "b": 17, "operation": "multiply"})
			} else if call == 2 {
				if !jsonContainsText(body, "714") {
					t.Error("first tool result missing")
				}
				writeMatrixToolCall(t, w, model.API, "math00002", "math_operation", JsonObject{"a": 453, "b": 434, "operation": "add"})
			} else {
				if !jsonContainsText(body, "887") {
					t.Error("second tool result missing")
				}
				writeMatrixUsageResponse(t, w, model.API, "714 and 887", true)
			}
		case "bedrockSpecial":
			writeMatrixUsageResponse(t, w, model.API, "hi", false)
		default:
			t.Fatalf("unknown upstream helper %s", tc.Kind)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			var body any
			if err := conn.ReadJSON(&body); err != nil {
				t.Error(err)
				return
			}
			recorder := httptest.NewRecorder()
			reply(recorder, body)
			for block := range strings.SplitSeq(recorder.Body.String(), "\n\n") {
				if value, ok := strings.CutPrefix(block, "data: "); ok && value != "[DONE]" {
					if err := conn.WriteMessage(websocket.TextMessage, []byte(value)); err != nil {
						t.Error(err)
						return
					}
				}
			}
			return
		}
		if options.Transport == TransportWebSocket {
			t.Error("explicit WebSocket request fell back to HTTP")
		}
		body, err := decodeMatrixRequest(r)
		if err != nil {
			t.Error(err)
			return
		}
		reply(w, body)
	}))
	t.Cleanup(server.Close)
	provider := newMatrixProvider(t, &model, server.URL, strings.HasPrefix(options.APIKey, "oauth:"))
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	request := Context{}
	switch tc.Kind {
	case "basicTextGeneration":
		request = Context{SystemPrompt: "You are a helpful assistant. Be concise.", Messages: []Message{UserMessage{Content: UserText("Reply with exactly: 'Hello test successful'"), Timestamp: 1}}}
	case "handleToolCall":
		request = Context{SystemPrompt: "You are a helpful assistant that uses tools when asked.", Tools: []ToolSchema{streamCalculatorTool()}, Messages: []Message{UserMessage{Content: UserText("Calculate 15 + 27 using the math_operation tool."), Timestamp: 1}}}
	case "handleStreaming":
		request = Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Count from 1 to 3"), Timestamp: 1}}}
	case "handleThinking":
		request = Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Think long and hard about 17 + 27. Think step by step. Then output the result."), Timestamp: 1}}}
	case "handleImage":
		request = Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "What do you see in this image? Please describe the shape (circle, rectangle, square, triangle, ...) and color (red, blue, green, ...). You MUST reply in English."}, ImageContent{Data: image, MimeType: "image/png"}}, Timestamp: 1}}}
	case "multiTurn":
		request = Context{SystemPrompt: "You are a helpful assistant that can use tools to answer questions.", Tools: []ToolSchema{streamCalculatorTool()}, Messages: []Message{UserMessage{Content: UserText("Think about this briefly, then calculate 42 * 17 and 453 + 434 using the math_operation tool."), Timestamp: 1}}}
	case "bedrockSpecial":
		var raw struct {
			SystemPrompt string
			Tools        []ToolSchema
			Messages     []struct {
				Content   string
				Timestamp int64
			}
		}
		if err := json.Unmarshal(tc.Request, &raw); err != nil {
			t.Fatal(err)
		}
		request.SystemPrompt = raw.SystemPrompt
		request.Tools = raw.Tools
		for _, m := range raw.Messages {
			request.Messages = append(request.Messages, UserMessage{Content: UserText(m.Content), Timestamp: m.Timestamp})
		}
	}
	var payloadCalls atomic.Int32
	if tc.Kind == "bedrockSpecial" {
		options.OnPayload = func(payload any, _ *Model) (any, error) {
			payloadCalls.Add(1)
			input, ok := payload.(*bedrockruntime.ConverseStreamInput)
			if !ok {
				t.Fatalf("payload = %T", payload)
			}
			observed := map[string]any{}
			if input.AdditionalModelRequestFields != nil {
				data, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
				if err != nil {
					return nil, err
				}
				var fields any
				if err := json.Unmarshal(data, &fields); err != nil {
					return nil, err
				}
				observed["additionalModelRequestFields"] = fields
			}
			if input.RequestMetadata != nil {
				encoded, err := json.Marshal(input.RequestMetadata)
				if err != nil {
					return nil, err
				}
				var value any
				if err := json.Unmarshal(encoded, &value); err != nil {
					return nil, err
				}
				observed["requestMetadata"] = value
			}
			validateStreamNativeOptions(t, model, rawOptions, observed, tc)
			return nil, nil
		}
	}
	stream := func() *AssistantMessageEventStream {
		s, err := provider.Stream(t.Context(), NormalizeContext(request), options)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	switch tc.Kind {
	case "basicTextGeneration":
		first := stream().Result()
		assertStreamTextGeneration(t, first, "Hello test successful")
		request.Messages = append(request.Messages, *first, UserMessage{Content: UserText("Now say 'Goodbye test successful'"), Timestamp: 2})
		assertStreamTextGeneration(t, stream().Result(), "Goodbye test successful")
	case "handleToolCall":
		assertStreamToolCall(t, stream())
	case "handleStreaming", "handleThinking":
		s := stream()
		start, end := false, false
		chunks := ""
		for event := range s.Events(t.Context()) {
			switch event := event.(type) {
			case TextStartEvent:
				if tc.Kind == "handleStreaming" {
					start = true
				}
			case TextDeltaEvent:
				if tc.Kind == "handleStreaming" {
					chunks += event.Delta
				}
			case TextEndEvent:
				if tc.Kind == "handleStreaming" {
					end = true
				}
			case ThinkingStartEvent:
				if tc.Kind == "handleThinking" {
					start = true
				}
			case ThinkingDeltaEvent:
				if tc.Kind == "handleThinking" {
					chunks += event.Delta
				}
			case ThinkingEndEvent:
				if tc.Kind == "handleThinking" {
					end = true
				}
			}
		}
		response := s.Result()
		found := false
		for _, block := range response.Content {
			switch block.(type) {
			case TextContent:
				if tc.Kind == "handleStreaming" {
					found = true
				}
			case ThinkingContent:
				if tc.Kind == "handleThinking" {
					found = true
				}
			}
		}
		if !start || !end || chunks == "" || !found {
			t.Fatalf("stream start=%t end=%t chunks=%q block=%t response=%+v", start, end, chunks, found, response)
		}
		if tc.Kind == "handleThinking" && response.StopReason != StopReasonStop {
			t.Fatalf("thinking stop=%s error=%s", response.StopReason, response.ErrorMessage)
		}
	case "handleImage":
		response := stream().Result()
		if len(response.Content) == 0 {
			t.Fatal("empty image response")
		}
		for _, block := range response.Content {
			if text, ok := block.(TextContent); ok {
				lower := strings.ToLower(text.Text)
				if !strings.Contains(lower, "red") || !strings.Contains(lower, "circle") {
					t.Fatalf("image description = %q", text.Text)
				}
				break
			}
		}
	case "multiTurn":
		allText := ""
		seen := false
		for turn := range 5 {
			response := stream().Result()
			request.Messages = append(request.Messages, *response)
			for _, block := range response.Content {
				switch block := block.(type) {
				case TextContent:
					allText += block.Text
				case ThinkingContent:
					seen = true
				case ToolCall:
					seen = true
					if block.Name != "math_operation" || block.ID == "" || block.Arguments == nil {
						t.Fatalf("invalid tool call %+v", block)
					}
					a, aok := block.Arguments["a"].(float64)
					b, bok := block.Arguments["b"].(float64)
					if !aok || !bok {
						t.Fatal("Invalid math arguments")
					}
					value := float64(0)
					switch block.Arguments["operation"] {
					case "add":
						value = a + b
					case "multiply":
						value = a * b
					}
					request.Messages = append(request.Messages, ToolResultMessage{ToolCallID: block.ID, ToolName: block.Name, Content: []ToolResultMessageContent{TextContent{Text: fmt.Sprint(value)}}, Timestamp: int64(turn + 2)})
				}
			}
			if response.StopReason == StopReasonError {
				t.Fatal(response.ErrorMessage)
			}
			if response.StopReason == StopReasonStop {
				break
			}
		}
		if !seen || allText == "" || !strings.Contains(allText, "714") || !strings.Contains(allText, "887") {
			t.Fatalf("multi-turn seen=%t text=%q", seen, allText)
		}
	case "bedrockSpecial":
		if response := stream().Result(); response.StopReason == StopReasonError {
			t.Fatal(response.ErrorMessage)
		}
		if payloadCalls.Load() != 1 {
			t.Fatalf("onPayload calls = %d, want 1", payloadCalls.Load())
		}
	}
}

func streamCalculatorTool() ToolSchema {
	return ToolSchema{Name: "math_operation", Description: "Perform basic arithmetic operations", Parameters: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "number", "description": "First number"}, "b": map[string]any{"type": "number", "description": "Second number"}, "operation": map[string]any{"type": "string", "enum": []string{"add", "subtract", "multiply", "divide"}, "description": "The operation to perform. One of 'add', 'subtract', 'multiply', 'divide'."}}, "required": []string{"a", "b", "operation"}}}
}
func assertStreamTextGeneration(t *testing.T, response *AssistantMessage, want string) {
	t.Helper()
	if response.messageRole() != "assistant" || response.Content == nil || response.Usage.Input+response.Usage.CacheRead <= 0 || response.Usage.Output <= 0 || response.ErrorMessage != "" || !strings.Contains(ContentText(response.Content, ""), want) {
		t.Fatalf("text generation = %+v; want %q", response, want)
	}
}
func assertStreamToolCall(t *testing.T, s *AssistantMessageEventStream) {
	t.Helper()
	start, delta, end := false, false, false
	index := 0
	args := ""
	for event := range s.Events(t.Context()) {
		var partial *AssistantMessage
		current := -1
		switch event := event.(type) {
		case ToolCallStartEvent:
			start = true
			index = event.ContentIndex
			partial, current = event.Partial, event.ContentIndex
		case ToolCallDeltaEvent:
			delta = true
			args += event.Delta
			partial, current = event.Partial, event.ContentIndex
		case ToolCallEndEvent:
			end = true
			partial, current = event.Partial, event.ContentIndex
		}
		if current >= 0 {
			if current != index {
				t.Fatal("tool content index changed")
			}
			call, ok := partial.Observe().Content[current].(ToolCall)
			if !ok || call.Name != "math_operation" || call.ID == "" || call.Arguments == nil {
				t.Fatalf("invalid partial tool %+v", partial)
			}
			if end {
				var decoded any
				if err := json.Unmarshal([]byte(args), &decoded); err != nil {
					t.Fatal(err)
				}
				a, aok := streamNumber(call.Arguments["a"])
				b, bok := streamNumber(call.Arguments["b"])
				if !aok || !bok || a != 15 || b != 27 || !slices.Contains([]any{"add", "subtract", "multiply", "divide"}, call.Arguments["operation"]) {
					t.Fatalf("arguments = %#v", call.Arguments)
				}
			}
		}
	}
	response := s.Result()
	if !start || !delta || !end || response.StopReason != StopReasonToolUse {
		t.Fatalf("tool events=%t/%t/%t result=%+v", start, delta, end, response)
	}
	found := false
	for _, block := range response.Content {
		if call, ok := block.(ToolCall); ok {
			found = true
			if call.Name != "math_operation" || call.ID == "" {
				t.Fatalf("invalid final tool %+v", call)
			}
		}
	}
	if !found {
		t.Fatal("No tool call found in response")
	}
}

func validateStreamNativeOptions(t *testing.T, model GeneratedModel, options map[string]any, raw any, tc upstreamStreamCase) {
	t.Helper()
	body, ok := raw.(map[string]any)
	if !ok {
		t.Fatal("non-object request")
	}
	if model.API == APIAnthropicMessages && options["thinkingEnabled"] == true {
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "enabled" && thinking["type"] != "adaptive" {
			t.Errorf("raw thinking enable lost: %#v", thinking)
		}
		if budget, ok := options["thinkingBudgetTokens"]; ok && thinking["type"] == "enabled" && thinking["budget_tokens"] != budget {
			t.Errorf("budget = %#v, want %#v", thinking, budget)
		}
		if effort, ok := options["effort"]; ok {
			cfg, _ := body["output_config"].(map[string]any)
			if cfg["effort"] != effort {
				t.Errorf("effort = %#v, want %v", cfg, effort)
			}
		}
	}
	if tc.Kind == "bedrockSpecial" {
		if strings.Contains(tc.Name, "adaptive thinking") {
			fields, _ := body["additionalModelRequestFields"].(map[string]any)
			// .upstream/v0.99.1/packages/ai/test/stream.test.ts:1544 expects max (:1583), but .upstream/v0.99.1/packages/ai/src/api/bedrock-converse-stream.ts:790-809 returns high for the unmapped xhigh level on Opus 4.6. The direct onPayload probe in bedrock-upstream-inconsistency.log confirms high; the upstream suite is credential-gated at stream.test.ts:1541.
			if !reflect.DeepEqual(fields["thinking"], map[string]any{"type": "adaptive", "display": "summarized"}) || !reflect.DeepEqual(fields["output_config"], map[string]any{"effort": "high"}) {
				t.Errorf("adaptive fields = %#v", fields)
			}
			if _, exists := fields["anthropic_beta"]; exists {
				t.Error("adaptive request has anthropic_beta")
			}
		} else if want, exists := options["requestMetadata"]; exists {
			if !reflect.DeepEqual(body["requestMetadata"], want) {
				t.Errorf("requestMetadata=%#v want %#v", body["requestMetadata"], want)
			}
		} else if _, exists := body["requestMetadata"]; exists {
			t.Error("unexpected requestMetadata")
		}
	}
}
