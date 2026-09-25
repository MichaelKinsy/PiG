package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pinned Responses, Codex, and Azure request builders read native tool flags
// from model.compat. Exercise the CLI resolver and runtime model constructor,
// not a provider configured directly by the test.
func TestModelConstructorsPreserveNativeResponsesCompat(t *testing.T) {
	for _, provider := range []string{"openai", "openai-codex", "azure-openai-responses"} {
		for _, capability := range []string{"supportsAdditionalTools", "supportsToolSearch"} {
			for _, constructor := range []string{"cli", "runtime"} {
				t.Run(provider+"/"+capability+"/"+constructor, func(t *testing.T) {
					apiKey := nativeResponsesTestAPIKey(provider)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if provider == "azure-openai-responses" {
							if r.Header.Get("api-key") != apiKey {
								t.Errorf("Azure api-key = %q", r.Header.Get("api-key"))
							}
						} else if r.Header.Get("Authorization") != "Bearer "+apiKey {
							t.Errorf("authorization = %q", r.Header.Get("Authorization"))
						}
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
					}))
					defer server.Close()
					dir := t.TempDir()
					t.Setenv("PIG_HOME", dir)
					api := provider
					switch provider {
					case "openai":
						api = "openai-responses"
					case "openai-codex":
						api = "openai-codex-responses"
					}
					config := fmt.Sprintf(`{"providers":{%q:{"baseUrl":%q,"apiKey":%q,"api":%q,"models":[{"id":"native-constructor-fixture","name":"Native fixture","reasoning":true,"compat":{"supportsMidConvoSystemMessages":true,%q:true}}]}}}`, provider, server.URL, apiKey, api, capability)
					if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
						t.Fatal(err)
					}
					var model *ai.Model
					var err error
					if constructor == "cli" {
						model, _, _, err = resolveModel("native-constructor-fixture", provider, codingagent.Settings{}, codingagent.NewModelRegistry(dir))
					} else {
						services, e := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: dir})
						if e != nil {
							t.Fatal(e)
						}
						model, err = coding.BuildModel(provider+"/native-constructor-fixture", services)
					}
					if err != nil {
						t.Fatal(err)
					}
					schema := func(name string) ai.ToolSchema {
						return ai.ToolSchema{Name: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
					}
					var body map[string]any
					stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{
						ai.SystemMessage{Content: ai.SystemText("base"), ToolsAdded: []ai.ToolSchema{schema("initial")}},
						ai.UserMessage{Content: ai.UserText("hello")},
						ai.SystemMessage{Content: ai.SystemText("later instructions"), ToolsAdded: []ai.ToolSchema{schema("later")}},
					}}), ai.StreamOptions{
						Transport: ai.TransportSSE,
						OnPayload: func(payload any, _ *ai.Model) (any, error) {
							encoded, marshalErr := json.Marshal(payload)
							if marshalErr == nil {
								marshalErr = json.Unmarshal(encoded, &body)
							}
							return nil, marshalErr
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					if result := stream.Result(); result.StopReason == ai.StopReasonError {
						t.Fatal(result.ErrorMessage)
					}
					tools, ok := body["tools"].([]any)
					if !ok || len(tools) != 1 || tools[0].(map[string]any)["name"] != "initial" {
						t.Fatalf("request tools = %#v; native compat lost", body["tools"])
					}
					expected := "additional_tools"
					if capability == "supportsToolSearch" {
						expected = "tool_search_output"
					}
					var count int
					for _, raw := range body["input"].([]any) {
						item := raw.(map[string]any)
						if item["type"] == expected {
							count++
							added := item["tools"].([]any)
							if len(added) != 1 || added[0].(map[string]any)["name"] != "later" {
								t.Fatalf("native tools = %#v", added)
							}
						}
					}
					if count != 1 {
						t.Fatalf("native %s count=%d; input=%#v", expected, count, body["input"])
					}
				})
			}
		}
	}
}

func nativeResponsesTestAPIKey(provider string) string {
	if provider != "openai-codex" {
		return "native-test-key"
	}
	claims, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "native-test-account"},
	})
	return "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}
