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

// Pi 406e719a6bff (#12049, post-v1.5.2, packages/ai/src/api/constrained-sampling.ts makeJsonSchemaNodeStrict): OpenAI strict mode rejects a schema
// node without a type, so a tool with strict "prefer" falls back to a non-strict definition and one with strict "require" fails locally; a node with
// only an enum stays strict. makeStrictJsonSchema is shared, so every API that resolves strict sampling carries the fallback. Pi's own test drives
// convertResponsesTools alone; these cases read the serialized request each API sends, so the strict marker and the schema on the wire are asserted.

type strictWireAPI struct {
	name   string
	api    func() *ProviderStreams
	model  string
	apiKey string
	compat *ModelCompat
	// wire returns whether the request marks tool 0 strict and the tool's JSON schema.
	wire func(t *testing.T, body map[string]any) (strict bool, schema map[string]any)
}

func asObject(t *testing.T, value any, what string) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want an object", what, value)
	}
	return object
}

func asFirst(t *testing.T, value any, what string) map[string]any {
	t.Helper()
	list, ok := value.([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("%s = %#v, want a non-empty array", what, value)
	}
	return asObject(t, list[0], what+"[0]")
}

func strictWireAPIs() []strictWireAPI {
	yes := true
	flat := func(t *testing.T, body map[string]any) (bool, map[string]any) {
		tool := asFirst(t, body["tools"], "tools")
		return tool["strict"] == true, asObject(t, tool["parameters"], "tools[0].parameters")
	}
	nested := func(t *testing.T, body map[string]any) (bool, map[string]any) {
		function := asObject(t, asFirst(t, body["tools"], "tools")["function"], "tools[0].function")
		return function["strict"] == true, asObject(t, function["parameters"], "tools[0].function.parameters")
	}
	google := func(t *testing.T, body map[string]any) (bool, map[string]any) {
		declaration := asFirst(t, asFirst(t, body["tools"], "tools")["functionDeclarations"], "functionDeclarations")
		config, _ := body["toolConfig"].(map[string]any)
		calling, _ := config["functionCallingConfig"].(map[string]any)
		return calling["mode"] == "VALIDATED", asObject(t, declaration["parametersJsonSchema"], "parametersJsonSchema")
	}
	return []strictWireAPI{
		{name: "openai-completions", api: OpenAICompletionsAPI, model: "gpt-4o-mini", apiKey: "k", compat: &ModelCompat{SupportsStrictMode: &yes}, wire: nested},
		{name: "openai-responses", api: OpenAIResponsesAPI, model: "gpt-5.1", apiKey: "k", compat: &ModelCompat{SupportsStrictMode: &yes}, wire: flat},
		{name: "azure-openai-responses", api: AzureOpenAIResponsesAPI, model: "gpt-4o", apiKey: "k", wire: flat},
		{name: "openai-codex-responses", api: OpenAICodexResponsesAPI, model: "gpt-5.1-codex", apiKey: codexToken(), wire: flat},
		{name: "anthropic-messages", api: AnthropicMessagesAPI, model: "claude-sonnet-4-5", apiKey: "k", compat: &ModelCompat{SupportsStrictTools: &yes}, wire: func(t *testing.T, body map[string]any) (bool, map[string]any) {
			tool := asFirst(t, body["tools"], "tools")
			return tool["strict"] == true, asObject(t, tool["input_schema"], "tools[0].input_schema")
		}},
		{name: "mistral-conversations", api: MistralConversationsAPI, model: "mistral-large-latest", apiKey: "k", compat: &ModelCompat{SupportsStrictMode: &yes}, wire: nested},
		{name: "google-generative-ai", api: GoogleGenerativeAIAPI, model: "gemini-3-pro-preview", apiKey: "k", wire: google},
		{name: "google-vertex", api: GoogleVertexAPI, model: "gemini-3-pro-preview", apiKey: "k", wire: google},
		{name: "bedrock-converse-stream", api: BedrockConverseStreamAPI, model: "anthropic.claude-3-haiku", apiKey: "k", compat: &ModelCompat{SupportsStrictMode: &yes}, wire: func(t *testing.T, body map[string]any) (bool, map[string]any) {
			config := asObject(t, body["toolConfig"], "toolConfig")
			spec := asObject(t, asFirst(t, config["tools"], "toolConfig.tools")["toolSpec"], "toolSpec")
			return spec["strict"] == true, asObject(t, asObject(t, spec["inputSchema"], "inputSchema")["json"], "inputSchema.json")
		}},
	}
}

// requestOfTool sends one request with the tool and returns the first request body the endpoint saw (nil when it saw none) and the stream's result.
func requestOfTool(t *testing.T, api strictWireAPI, tool ToolSchema) (map[string]any, AssistantMessage) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_REGION", "us-east-1")
	var first map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") == "zstd" {
			raw, _ = decodeZstdFrameForTest(raw)
		}
		if first == nil {
			_ = json.Unmarshal(raw, &first)
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	model := &Model{ID: api.model, DisplayName: api.model, Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIAnthropicMessages, ProviderID: "p", BaseURL: server.URL, Compat: api.compat}}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}, Tools: []ToolSchema{tool}})
	stream, err := api.api().Stream(t.Context(), model, transcript, StreamOptions{APIKey: api.apiKey, Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	return first, *stream.Result()
}

func strictTestTool(t *testing.T, parameters, strict string) ToolSchema {
	t.Helper()
	tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":`+parameters+`,"constrainedSampling":{"type":"json_schema","strict":"`+strict+`"}}]`)
	return tools[0]
}

func TestUntypedSchemaNodesFallBackFromStrictInTheRequestPort(t *testing.T) {
	untyped := []struct{ name, parameters, properties string }{
		// Type.Object({ commandArguments: Type.Optional(Type.Array(Type.Unknown())) }), the schema of issue #12049.
		{"array of unknown items", `{"type":"object","properties":{"commandArguments":{"type":"array","items":{}}}}`, `{"commandArguments":{"type":"array","items":{}}}`},
		// Type.Object({ value: Type.Any({ description: "Any JSON value" }) })
		{"any", `{"type":"object","required":["value"],"properties":{"value":{"description":"Any JSON value"}}}`, `{"value":{"description":"Any JSON value"}}`},
		{"bare const", `{"type":"object","properties":{"mode":{"const":"fast"}},"required":["mode"]}`, `{"mode":{"const":"fast"}}`},
		// A node below an anyOf variant and below a typed array is checked like any other.
		{"untyped anyOf variant", `{"type":"object","properties":{"v":{"anyOf":[{"type":"string"},{}]}},"required":["v"]}`, `{"v":{"anyOf":[{"type":"string"},{}]}}`},
	}
	for _, api := range strictWireAPIs() {
		if !BuiltAPI(API(api.name)) {
			// A build with this API's pig_strip tag has no provider for the row.
			continue
		}
		t.Run(api.name, func(t *testing.T) {
			// The control: a typed schema is sent strict, so the fixture model really reaches strict mode.
			body, result := requestOfTool(t, api, strictTestTool(t, `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`, "prefer"))
			if body == nil {
				t.Fatalf("no request for the control tool: %#v", result)
			}
			if strict, schema := api.wire(t, body); !strict || schema["additionalProperties"] != false {
				t.Fatalf("control tool strict=%v schema=%v, want a strict schema", strict, schema)
			}
			for _, tc := range untyped {
				t.Run(tc.name+" prefer", func(t *testing.T) {
					body, result := requestOfTool(t, api, strictTestTool(t, tc.parameters, "prefer"))
					if body == nil {
						t.Fatalf("no request: %#v", result)
					}
					strict, schema := api.wire(t, body)
					if strict {
						t.Errorf("the request marks the tool strict: %v", body)
					}
					var want map[string]any
					if err := json.Unmarshal([]byte(tc.properties), &want); err != nil {
						t.Fatal(err)
					}
					if strings.HasPrefix(api.name, "google") {
						want = constAsEnumForGoogle(want)
					}
					if !reflect.DeepEqual(schema["properties"], any(want)) {
						t.Errorf("properties = %v, want the authored %v", schema["properties"], want)
					}
					if _, rewritten := schema["additionalProperties"]; rewritten {
						t.Errorf("the schema was rewritten for strict mode: %v", schema)
					}
				})
				t.Run(tc.name+" require", func(t *testing.T) {
					body, result := requestOfTool(t, api, strictTestTool(t, tc.parameters, "require"))
					if body != nil {
						t.Errorf("a request was sent for a tool that cannot be strict: %v", body)
					}
					if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, `Tool "lookup" requires JSON-schema constrained sampling, but schemas without a type are unsupported.`) {
						t.Errorf("result = %v %q", result.StopReason, result.ErrorMessage)
					}
				})
			}
			// Pi's "keeps strict mode for enum-only schemas": a node with an enum and no type stays strict.
			body, result = requestOfTool(t, api, strictTestTool(t, `{"type":"object","required":["level"],"properties":{"level":{"enum":["low","high"]}}}`, "prefer"))
			if body == nil {
				t.Fatalf("no request for the enum tool: %#v", result)
			}
			if strict, schema := api.wire(t, body); !strict || schema["additionalProperties"] != false || !reflect.DeepEqual(schema["properties"], map[string]any{"level": map[string]any{"enum": []any{"low", "high"}}}) {
				t.Errorf("enum-only tool strict=%v schema=%v, want strict with the enum node unchanged", strict, schema)
			}
		})
	}
}

// constAsEnumForGoogle is what google-shared.ts rewriteConstAsEnum makes of a non-strict parametersJsonSchema: Gemini ignores const there but honors enum.
func constAsEnumForGoogle(properties map[string]any) map[string]any {
	out := map[string]any{}
	for name, property := range properties {
		node, _ := property.(map[string]any)
		if value, isConst := node["const"]; isConst && len(node) == 1 {
			out[name] = map[string]any{"enum": []any{value}}
			continue
		}
		out[name] = property
	}
	return out
}
