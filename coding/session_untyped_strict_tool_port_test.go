package coding

// pi: packages/ai/src/api/constrained-sampling.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 406e719a6bff (#12049, post-v1.5.2): a tool that asks for strict sampling "prefer" but has a schema node without a type is sent without strict
// mode, and one that "require"s it fails locally. The boundary here is a session: an SDK tool registered with the session reaches a provider of each
// API kind through models.json, and the request the endpoint receives carries the fallback for that tool only; the built-in tools, which prefer
// strict sampling with typed schemas, and a typed tool of the same session stay strict.
func TestSessionSendsAStrictPreferToolWithAnUntypedNodeWithoutStrictPort(t *testing.T) {
	const untypedSchema = `{"type":"object","properties":{"commandArguments":{"type":"array","items":{}}}}`
	const typedSchema = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	type wireTool struct {
		strict bool
		schema map[string]any
	}
	toolsOf := func(t *testing.T, body map[string]any, nested bool) map[string]wireTool {
		t.Helper()
		out := map[string]wireTool{}
		list, _ := body["tools"].([]any)
		for _, item := range list {
			tool, _ := item.(map[string]any)
			if nested {
				tool, _ = tool["function"].(map[string]any)
			}
			schema, _ := tool["parameters"].(map[string]any)
			if schema == nil {
				schema, _ = tool["input_schema"].(map[string]any)
			}
			name, _ := tool["name"].(string)
			out[name] = wireTool{strict: tool["strict"] == true, schema: schema}
		}
		return out
	}
	for _, tc := range []struct {
		api    ai.API
		compat string
		nested bool
	}{
		{ai.APIOpenAIResponses, `{"supportsStrictMode":true}`, false},
		{ai.APIOpenAICompletions, `{"supportsStrictMode":true}`, true},
		{ai.APIAnthropicMessages, `{"supportsStrictTools":true}`, false},
	} {
		t.Run(string(tc.api), func(t *testing.T) {
			var first map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if first == nil {
					_ = json.Unmarshal(raw, &first)
				}
				http.Error(w, "stop", http.StatusBadRequest)
			}))
			defer server.Close()
			agentDir := t.TempDir()
			config := fmt.Sprintf(`{"providers":{"hooked":{"api":%q,"baseUrl":%q,"apiKey":"k","models":[{"id":"hook-model","name":"Hook","input":["text"],"contextWindow":100000,"maxTokens":8000,"compat":%s}]}}}`, tc.api, server.URL, tc.compat)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("hooked/hook-model", services)
			if err != nil {
				t.Fatal(err)
			}
			tool := func(name, schema, strict string) extension.ToolDefinition {
				return extension.ToolDefinition{Name: name, Label: name, Description: name, Parameters: json.RawMessage(schema), ConstrainedSampling: json.RawMessage(`{"type":"json_schema","strict":"` + strict + `"}`),
					Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
						return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
					}}
			}
			prompt := func(t *testing.T, custom ...extension.ToolDefinition) (map[string]any, string) {
				t.Helper()
				first = nil
				session, err := NewSession(services, SessionOptions{Model: model, SessionDir: t.TempDir(), CustomTools: custom})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				if err := session.Prompt(t.Context(), "hi"); err != nil {
					t.Fatal(err)
				}
				failure := ""
				for _, message := range session.Messages() {
					if message.Assistant != nil {
						failure = message.Assistant.ErrorMessage
					}
				}
				return first, failure
			}

			body, _ := prompt(t, tool("typed_tool", typedSchema, "prefer"), tool("untyped_tool", untypedSchema, "prefer"))
			if body == nil {
				t.Fatal("the endpoint received no request")
			}
			wire := toolsOf(t, body, tc.nested)
			for _, name := range []string{"read", "bash", "edit", "write", "typed_tool"} {
				if got, ok := wire[name]; !ok || !got.strict || got.schema["additionalProperties"] != false {
					t.Errorf("%s = %+v, want a strict schema (the tools of the request: %v)", name, got, reflect.ValueOf(wire).MapKeys())
				}
			}
			untyped, ok := wire["untyped_tool"]
			if !ok {
				t.Fatalf("the request lacks untyped_tool: %v", body["tools"])
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(untypedSchema), &want); err != nil {
				t.Fatal(err)
			}
			if untyped.strict || !reflect.DeepEqual(untyped.schema["properties"], want["properties"]) || untyped.schema["additionalProperties"] != nil {
				t.Errorf("untyped_tool = %+v, want a non-strict tool with its authored properties %v", untyped, want["properties"])
			}

			body, failure := prompt(t, tool("untyped_tool", untypedSchema, "require"))
			if body != nil {
				t.Errorf("a request was sent although untyped_tool cannot be strict: %v", body)
			}
			if !strings.Contains(failure, `Tool "untyped_tool" requires JSON-schema constrained sampling, but schemas without a type are unsupported.`) {
				t.Errorf("the assistant message's error = %q", failure)
			}
		})
	}
}
