package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// Pi openai-completions.ts:713 and openai-responses.ts:207-210 normalize SDK errors through utils/error-body.ts.
func TestOpenAIHTTPErrorMatchesPi(t *testing.T) {
	t.Parallel()
	bodies := []string{`{"error":{"message":"Authentication Fails","type":"authentication_error","param":null,"code":"invalid_request_error"}}`, `{"error":{"error":"blocked by gateway WAF"}}`, `{"error":{"message":"Provider returned error","code":403,"metadata":{"raw":"upstream WAF blocked policy XYZ"}}}`, `{"error":{"message":""}}`, `{"error":{}}`, `{"message":"outside error"}`, `proxy failure`, "", `{"error":"plain error"}`, `{"error":{"message":["one","two"]}}`, `{"error":{"message":"long","body":"` + strings.Repeat("界", 4100) + `"}}`}
	bodies = append(bodies, `{"error":{"message":"bad","z":1,"1":2,"a":"\u754c","ratio":1.0}}`, `{"error":{"message":0.0}}`, "false")
	// openai 7.x makeStatusError makes an object or array body whose `error` member is null or absent the error object itself.
	bodies = append(bodies, `{"error":null,"detail":"gateway"}`, `{}`, `[]`, `["one",{"two":2}]`, `{"message":{"nested":true}}`, `"quoted body"`, `7`, `{"error":false,"message":"kept off"}`)
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses} {
		for _, provider := range []string{"openai", "custom-proxy"} {
			for i, body := range bodies {
				t.Run(string(api)+"/"+provider+"/"+string(rune('a'+i)), func(t *testing.T) {
					t.Parallel()
					status := http.StatusUnauthorized
					prefix := ""
					if api != APIOpenAICompletions {
						name := provider
						if api == APIAzureOpenAIResponses {
							name = "Azure OpenAI"
						}
						if name == "openai" {
							name = "OpenAI"
						}
						prefix = name + " API error"
					}
					probe := map[string]any{"status": status, "body": body}
					if prefix != "" {
						probe["prefix"] = prefix
					}
					input, err := json.Marshal(probe)
					if err != nil {
						t.Fatal(err)
					}
					cmd := exec.CommandContext(t.Context(), "node", "ai/testdata/openai-error-pi.mjs")
					cmd.Dir = ".."
					cmd.Stdin = bytes.NewReader(input)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("Pi: %v\n%s", err, output)
					}
					var want string
					if err := json.Unmarshal(output, &want); err != nil {
						t.Fatal(err)
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_, _ = w.Write([]byte(body))
					}))
					defer server.Close()
					var p Provider
					if api == APIOpenAICompletions {
						p = NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL, APIKey: "fake", Model: "test-model", ProviderID: provider})
					} else {
						p = NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "fake", Model: "test-model", ProviderID: provider, api: api})
					}
					result := transportErrorTestResult(t, p)
					if result.StopReason != StopReasonError || result.ErrorMessage != want {
						t.Fatalf("Go: %s\nPi: %s", result.ErrorMessage, want)
					}
				})
			}
		}
	}
}
