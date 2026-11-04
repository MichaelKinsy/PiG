//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

// Ports packages/ai/test/bedrock-strict-tool-fallback.test.ts ("Bedrock preferred strict tool fallback", Pi #11416; Pi 842c7317).
// Pi mocks the SDK client's send and keeps a structuredClone of each command input; the port serves the same replies on the wire and keeps each request body.

// strictToolFallbackUnionLimitMessage is observed from live Bedrock with 20 optional parameters across strict tools.
const strictToolFallbackUnionLimitMessage = "The model returned the following errors: Schemas contains too many parameters with union types (20 parameters with type arrays or anyOf). This causes exponential compilation cost. Reduce the number of nullable or union-typed parameters (limit: 16 parameters with unions)."

type strictToolFallbackSpec struct {
	Name        string `json:"name"`
	Strict      *bool  `json:"strict"`
	InputSchema struct {
		JSON json.RawMessage `json:"json"`
	} `json:"inputSchema"`
}

type strictToolFallbackRequest struct {
	ToolConfig struct {
		Tools []struct {
			ToolSpec strictToolFallbackSpec `json:"toolSpec"`
		} `json:"tools"`
	} `json:"toolConfig"`
	RequestMetadata map[string]string `json:"requestMetadata"`
	// tools is each tool of the request as the wire carries it.
	tools []json.RawMessage
}

// strictToolFallbackTool is Pi's tool(): an object with a required string and an optional number, constrained by strict when it is not empty.
func strictToolFallbackTool(t *testing.T, name, strict string) ai.ToolSchema {
	t.Helper()
	document := `{"name":"` + name + `","description":"Look up a value","parameters":{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"]}`
	if strict != "" {
		document += `,"constrainedSampling":{"type":"json_schema","strict":"` + strict + `"}`
	}
	tools := mustTools(t, "["+document+"}]")
	return tools[0]
}

// strictToolFallbackHarness serves each request with respond(attempt) and records the request bodies in order.
type strictToolFallbackHarness struct {
	mu       sync.Mutex
	requests []strictToolFallbackRequest
	provider *BedrockProvider
}

// strictToolFallbackReply is one reply: an SDK ValidationException with message, or the stream of one empty end_turn message.
type strictToolFallbackReply struct {
	validationMessage string
}

func newStrictToolFallbackHarness(t *testing.T, respond func(attempt int) strictToolFallbackReply) *strictToolFallbackHarness {
	t.Helper()
	h := &strictToolFallbackHarness{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var request strictToolFallbackRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Errorf("request body is not JSON: %v\n%s", err, raw)
		}
		var body struct {
			ToolConfig struct {
				Tools []json.RawMessage `json:"tools"`
			} `json:"toolConfig"`
		}
		_ = json.Unmarshal(raw, &body)
		request.tools = body.ToolConfig.Tools
		h.mu.Lock()
		h.requests = append(h.requests, request)
		attempt := len(h.requests)
		h.mu.Unlock()
		reply := respond(attempt)
		if reply.validationMessage != "" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Amzn-Errortype", "ValidationException")
			w.WriteHeader(http.StatusBadRequest)
			message, _ := json.Marshal(map[string]string{"message": reply.validationMessage})
			_, _ = w.Write(message)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		writeBedrockFrames(t, w, [][2]string{{"messageStart", `{"role":"assistant"}`}, {"messageStop", `{"stopReason":"end_turn"}`}})
	}))
	t.Cleanup(server.Close)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	model := ai.Model{
		ID:           "us.anthropic.claude-opus-5-5",
		DisplayName:  "Claude Opus 5.5 (US)",
		Input:        []string{"text"},
		Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 100},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "amazon-bedrock", API: ai.APIBedrockConverseStream, BaseURL: server.URL, Compat: &ai.ModelCompat{SupportsStrictMode: new(true)}},
	}
	h.provider = NewBedrockProviderWithModel(model)
	t.Cleanup(func() { _ = h.provider.Close() })
	return h
}

func (h *strictToolFallbackHarness) run(t *testing.T, tools []ai.ToolSchema, onPayload func(any, *ai.Model) (any, error)) *ai.AssistantMessage {
	t.Helper()
	stream, err := h.provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Tools: tools, Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello"), Timestamp: 1}}}), ai.StreamOptions{
		CacheRetention: ai.CacheRetentionNone,
		Env:            ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
		OnPayload:      onPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

func (h *strictToolFallbackHarness) snapshot() []strictToolFallbackRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]strictToolFallbackRequest(nil), h.requests...)
}

// Regression tests for https://github.com/earendil-works/pi/issues/11416.
func TestBedrockPreferredStrictToolFallbackUpstream(t *testing.T) {
	// bedrock-strict-tool-fallback.test.ts:81
	t.Run("retries a strict tool limit rejection with preferred tools non-strict and keeps required tools strict", func(t *testing.T) {
		h := newStrictToolFallbackHarness(t, func(attempt int) strictToolFallbackReply {
			if attempt == 1 {
				return strictToolFallbackReply{validationMessage: strictToolFallbackUnionLimitMessage}
			}
			return strictToolFallbackReply{}
		})
		onPayloadCalls := 0
		onPayload := func(payload any, _ *ai.Model) (any, error) {
			onPayloadCalls++
			input := payload.(*bedrockruntime.ConverseStreamInput)
			input.RequestMetadata = map[string]string{"team": "a"}
			return input, nil
		}

		result := h.run(t, []ai.ToolSchema{strictToolFallbackTool(t, "preferred", "prefer"), strictToolFallbackTool(t, "required", "require"), strictToolFallbackTool(t, "ordinary", "")}, onPayload)

		if result.StopReason != ai.StopReasonStop {
			t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
		}
		if onPayloadCalls != 2 {
			t.Fatalf("onPayload calls = %d, want 2", onPayloadCalls)
		}
		requests := h.snapshot()
		if len(requests) != 2 {
			t.Fatalf("requests = %d, want 2", len(requests))
		}
		first, second := requests[0], requests[1]
		firstSpec := first.ToolConfig.Tools[0].ToolSpec
		if firstSpec.Strict == nil || !*firstSpec.Strict {
			t.Fatalf("first request sent the preferred tool without strict: %s", first.tools[0])
		}
		// toMatchObject: the strict schema lists every property as required and makes the optional one nullable.
		var strictSchema struct {
			Required   json.RawMessage `json:"required"`
			Properties struct {
				Optional json.RawMessage `json:"optional"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(firstSpec.InputSchema.JSON, &strictSchema); err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, strictSchema.Required, `["value","optional"]`)
		assertShapeJSON(t, strictSchema.Properties.Optional, `{"anyOf":[{"type":"number"},{"type":"null"}]}`)
		secondSpec := second.ToolConfig.Tools[0].ToolSpec
		if secondSpec.Strict != nil {
			t.Fatalf("second request sent the preferred tool with strict=%t", *secondSpec.Strict)
		}
		assertShapeJSON(t, secondSpec.InputSchema.JSON, `{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"]}`)
		assertShapeJSON(t, second.tools[1], string(first.tools[1]))
		if required := second.ToolConfig.Tools[1].ToolSpec.Strict; required == nil || !*required {
			t.Fatalf("second request sent the required tool without strict: %s", second.tools[1])
		}
		assertShapeJSON(t, second.tools[2], string(first.tools[2]))
		if len(second.RequestMetadata) != 1 || second.RequestMetadata["team"] != "a" {
			t.Fatalf("second request metadata = %v, want the onPayload value", second.RequestMetadata)
		}
		if len(result.Diagnostics) != 1 {
			t.Fatalf("diagnostics = %#v, want the fallback diagnostic only", result.Diagnostics)
		}
		diagnostic := result.Diagnostics[0]
		if diagnostic.Type != "bedrock_strict_tools_fallback" || diagnostic.Error == nil || diagnostic.Error.Message != strictToolFallbackUnionLimitMessage || len(diagnostic.Details) != 1 || diagnostic.Details["reason"] != "strict_tool_limits_exceeded" {
			t.Fatalf("diagnostic = %#v", diagnostic)
		}
	})

	// bedrock-strict-tool-fallback.test.ts:119
	t.Run("does not retry without preferred strict tools", func(t *testing.T) {
		h := newStrictToolFallbackHarness(t, func(int) strictToolFallbackReply {
			return strictToolFallbackReply{validationMessage: strictToolFallbackUnionLimitMessage}
		})

		result := h.run(t, []ai.ToolSchema{strictToolFallbackTool(t, "required", "require"), strictToolFallbackTool(t, "ordinary", "")}, nil)

		if result.StopReason != ai.StopReasonError {
			t.Fatalf("stopReason = %s", result.StopReason)
		}
		if want := "Validation error: " + strictToolFallbackUnionLimitMessage; result.ErrorMessage != want {
			t.Fatalf("errorMessage = %q, want %q", result.ErrorMessage, want)
		}
		if requests := h.snapshot(); len(requests) != 1 {
			t.Fatalf("requests = %d, want 1", len(requests))
		}
	})

	// bedrock-strict-tool-fallback.test.ts:129
	t.Run("does not retry other validation errors", func(t *testing.T) {
		h := newStrictToolFallbackHarness(t, func(int) strictToolFallbackReply {
			return strictToolFallbackReply{validationMessage: "messages: field required"}
		})

		result := h.run(t, []ai.ToolSchema{strictToolFallbackTool(t, "preferred", "prefer")}, nil)

		if result.StopReason != ai.StopReasonError {
			t.Fatalf("stopReason = %s", result.StopReason)
		}
		if requests := h.snapshot(); len(requests) != 1 {
			t.Fatalf("requests = %d, want 1", len(requests))
		}
	})

	// bedrock-strict-tool-fallback.test.ts:138
	t.Run("retries only once", func(t *testing.T) {
		h := newStrictToolFallbackHarness(t, func(int) strictToolFallbackReply {
			return strictToolFallbackReply{validationMessage: strictToolFallbackUnionLimitMessage}
		})

		result := h.run(t, []ai.ToolSchema{strictToolFallbackTool(t, "preferred", "prefer")}, nil)

		if result.StopReason != ai.StopReasonError {
			t.Fatalf("stopReason = %s", result.StopReason)
		}
		if requests := h.snapshot(); len(requests) != 2 {
			t.Fatalf("requests = %d, want 2", len(requests))
		}
	})
}

// convertBedrockTools reports a strict: "prefer" tool sent strict (hasPreferredStrictTools) and, for the fallback, sends it without strict.
func TestBedrockConvertToolsPreferredStrictTools(t *testing.T) {
	tools := []ai.ToolSchema{strictToolFallbackTool(t, "preferred", "prefer"), strictToolFallbackTool(t, "required", "require"), strictToolFallbackTool(t, "ordinary", "")}
	strictOf := func(config *btypes.ToolConfiguration, index int) bool {
		return aws.ToBool(config.Tools[index].(*btypes.ToolMemberToolSpec).Value.Strict)
	}
	config, hasPreferred, err := convertBedrockTools(tools, nil, true, false)
	if err != nil || !hasPreferred || !strictOf(config, 0) || !strictOf(config, 1) || strictOf(config, 2) {
		t.Fatalf("strict request: config=%+v hasPreferred=%t err=%v", config, hasPreferred, err)
	}
	config, hasPreferred, err = convertBedrockTools(tools, nil, true, true)
	if err != nil || hasPreferred || strictOf(config, 0) || !strictOf(config, 1) || strictOf(config, 2) {
		t.Fatalf("fallback request: config=%+v hasPreferred=%t err=%v", config, hasPreferred, err)
	}
	if _, hasPreferred, err = convertBedrockTools(tools[:1], nil, false, false); err != nil || hasPreferred {
		t.Fatalf("a model without strict mode sends no strict tool: hasPreferred=%t err=%v", hasPreferred, err)
	}
	if config, hasPreferred, err = convertBedrockTools(tools, "none", true, false); err != nil || config != nil || hasPreferred {
		t.Fatalf("toolChoice none sends no tools: config=%+v hasPreferred=%t err=%v", config, hasPreferred, err)
	}
}
