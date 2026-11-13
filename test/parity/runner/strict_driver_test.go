//go:build parity

package runner

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRPCSignalTeardownKeepsStdinOpen(t *testing.T) {
	sc := &Scenario{Name: "signal-open", RPC: RPCDriverConfig{Terminate: true, TimeoutSeconds: 5, Steps: []RPCInputStep{{Line: "request", WaitEvent: `{"type":"ready"}`, WaitTimeoutSeconds: 1}}}}
	bin := BinaryRef{Label: "pig", Path: "sh", Args: []string{"-c", `trap 'printf "signal\n" >&2; exit 143' TERM; read -r line; printf '{"type":"ready"}\n'; while read -r line; do :; done; printf 'EOF\n' >&2; exit 7`}}
	result := (rpcModeDriver{}).Run(t.Context(), t, bin, sc)
	if result.Err != nil || result.ExitCode != 143 || result.Stderr != "signal\n" || !result.StderrCaptured {
		t.Fatalf("signal teardown lost open stdin or stderr: %+v", result)
	}
}

func TestRPCEventBarrierRequiresOneNewCompleteCorrelatedRecord(t *testing.T) {
	for _, tc := range []struct {
		output string
		pass   bool
	}{
		{"{\"type\":\"response\",\"id\":\"old\"}\n", false},
		{"{\"type\":\"response\",\"id\":\"new\"}", false},
		{"{\"type\":\"response\",\"id\":\"new\"} garbage\n", false},
		{"{\"type\":\"response\",\"id\":\"old\"}\n{\"type\":\"other\",\"id\":\"new\"}\n", false},
		{"{\"id\":\"new\",\"type\":\"response\"}\n", true},
	} {
		var b synchronizedBuffer
		_, _ = b.Write([]byte(tc.output))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := waitForRPCOutput(ctx, &b, 0, nil, `{"type":"response","id":"new"}`, 1)
		if (err == nil) != tc.pass {
			t.Fatalf("%q: %v", tc.output, err)
		}
	}
}

func TestProviderFixtureDrivesActualToolResultsAndHTTPFailure(t *testing.T) {
	// One row per API and turn kind. Request bodies follow each provider serializer: Pi openai-completions.ts and mistral-conversations.ts (messages), anthropic-messages.ts (user text / tool_result blocks), google-shared.ts:311-318 (functionResponse.response.output).
	for _, tc := range []struct {
		api      string
		body     string
		status   int
		contains string
	}{
		{"openai-completions", `{"messages":[{"role":"user","content":[{"type":"text","text":"READ"}]}]}`, 200, `"name":"read"`},
		{"openai-completions", `{"messages":[{"role":"tool","content":"actual  result\n"}]}`, 200, `"content":"actual  result\n"`},
		{"openai-completions", `{"messages":[{"role":"user","content":"HTTP_ERROR"}]}`, 400, `"message":"strict wire bad request"`},
		{"openai-responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"READ"}]}]}`, 200, `"item":{"arguments":"{\"path\":\"parity-read-target.txt\"}","call_id":"strict-read","id":"fc_strict_read","name":"read","status":"completed","type":"function_call"}`},
		{"openai-responses", `{"input":[{"role":"user","content":"READ"},{"type":"function_call","call_id":"strict-read","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"strict-read","output":"actual  result\n"}]}`, 200, `"delta":"actual  result\n"`},
		{"openai-responses", `{"input":[{"type":"function_call_output","call_id":"strict-read","output":[{"type":"input_text","text":"READ"}]}]}`, 200, `"content":[{"annotations":[],"text":"READ","type":"output_text"}]`},
		{"openai-responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"ECHO_REQUEST /tools"}]}],"tools":[{"type":"function","name":"z","strict":false}]}`, 200, `"delta":"[{\"type\":\"function\",\"name\":\"z\",\"strict\":false}]"`},
		{"openai-responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"HTTP_ERROR"}]}]}`, 400, `"message":"strict wire bad request"`},
		{"mistral-conversations", `{"messages":[{"role":"user","content":[{"type":"text","text":"READ"}]}]}`, 200, `"name":"read"`},
		{"mistral-conversations", `{"messages":[{"role":"tool","content":[{"type":"text","text":"actual  result\n"}]}]}`, 200, `"content":"actual  result\n"`},
		{"mistral-conversations", `{"messages":[{"role":"user","content":"HTTP_ERROR"}]}`, 400, `"message":"strict wire bad request"`},
		{"anthropic-messages", `{"messages":[{"role":"user","content":[{"type":"text","text":"READ"}]}]}`, 200, `"type":"tool_use"`},
		{"anthropic-messages", `{"messages":[{"role":"user","content":"READ"}]}`, 200, `"type":"tool_use"`},
		{"anthropic-messages", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"actual  result\n"}]}]}`, 200, `"text":"actual  result\n"`},
		{"anthropic-messages", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"READ"}]}]}]}`, 200, `"text":"READ"`},
		{"anthropic-messages", `{"messages":[{"role":"user","content":"HTTP_ERROR"}]}`, 400, `"message":"strict wire bad request"`},
		{"google-generative-ai", `{"contents":[{"role":"user","parts":[{"text":"READ"}]}]}`, 200, `"functionCall":{"args":{"path":"parity-read-target.txt"},"name":"read"}`},
		{"google-generative-ai", `{"contents":[{"role":"user","parts":[{"functionResponse":{"name":"read","response":{"output":"actual  result\n"}}}]}]}`, 200, `"text":"actual  result\n"`},
		{"google-generative-ai", `{"contents":[{"role":"user","parts":[{"text":"HTTP_ERROR"}]}]}`, 400, `"message":"strict wire bad request"`},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		providerFixtures[tc.api].handler().ServeHTTP(w, r)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s fixture output for %s: %d %s", tc.api, tc.body, w.Code, w.Body.String())
		}
	}
}

func TestProviderFixtureAPISelection(t *testing.T) {
	for _, tc := range []struct {
		sc      Scenario
		want    string
		wantErr bool
	}{
		{Scenario{}, "", false},
		{Scenario{OpenAIFixture: true}, "openai-completions", false},
		{Scenario{ProviderFixture: "anthropic-messages"}, "anthropic-messages", false},
		{Scenario{ProviderFixture: "openai-responses"}, "openai-responses", false},
		{Scenario{OpenAIFixture: true, ProviderFixture: "anthropic-messages"}, "", true},
		{Scenario{ProviderFixture: "no-such-api"}, "", true},
		{Scenario{ProviderFixture: "anthropic-messages", ProviderFixtureBasePath: "/api.anthropic.com"}, "anthropic-messages", false},
		{Scenario{OpenAIFixture: true, ProviderFixtureCompat: map[string]any{"supportsStrictMode": true}}, "openai-completions", false},
		{Scenario{ProviderFixture: "anthropic-messages", ProviderFixtureBasePath: "api.anthropic.com"}, "", true},
		{Scenario{ProviderFixture: "anthropic-messages", ProviderFixtureBasePath: "/api.anthropic.com/"}, "", true},
		{Scenario{ProviderFixtureBasePath: "/api.anthropic.com"}, "", true},
		{Scenario{ProviderFixtureCompat: map[string]any{"supportsStrictMode": true}}, "", true},
	} {
		got, err := providerFixtureAPI(&tc.sc)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Fatalf("providerFixtureAPI(%+v) = %q, %v", tc.sc, got, err)
		}
	}
}

// The model definition without the two scenario options is the one the fixture has always written; each option changes its own member only.
func TestProviderFixtureModels(t *testing.T) {
	const model = `{"contextWindow":128000,"cost":{"cacheRead":0,"cacheWrite":0,"input":0,"output":0},"id":"strict","input":["text"],"maxTokens":1000,"name":"strict","reasoning":false}`
	const withCompat = `{"compat":{"supportsMessageThreads":false,"supportsStrictMode":true},` + `"contextWindow":128000,"cost":{"cacheRead":0,"cacheWrite":0,"input":0,"output":0},"id":"strict","input":["text"],"maxTokens":1000,"name":"strict","reasoning":false}`
	provider := func(api, baseURL, model string) string {
		return `{"providers":{"strict-wire":{"api":"` + api + `","apiKey":"fixture-key","baseUrl":"` + baseURL + `","models":[` + model + `]}}}`
	}
	for _, tc := range []struct {
		sc   Scenario
		api  string
		want string
	}{
		{Scenario{}, "openai-completions", provider("openai-completions", "http://127.0.0.1:1/v1", model)},
		{Scenario{}, "anthropic-messages", provider("anthropic-messages", "http://127.0.0.1:1", model)},
		{Scenario{}, "google-generative-ai", provider("google-generative-ai", "http://127.0.0.1:1/v1beta", model)},
		{Scenario{ProviderFixtureBasePath: "/api.anthropic.com"}, "anthropic-messages", provider("anthropic-messages", "http://127.0.0.1:1/api.anthropic.com", model)},
		{Scenario{ProviderFixtureBasePath: "/proxy"}, "openai-responses", provider("openai-responses", "http://127.0.0.1:1/proxy/v1", model)},
		{Scenario{ProviderFixtureCompat: map[string]any{"supportsStrictMode": true, "supportsMessageThreads": false}}, "openai-completions", provider("openai-completions", "http://127.0.0.1:1/v1", withCompat)},
		{Scenario{ProviderFixtureCompat: map[string]any{}}, "mistral-conversations", provider("mistral-conversations", "http://127.0.0.1:1", model)},
	} {
		data, err := json.Marshal(providerFixtureModels(&tc.sc, tc.api, "http://127.0.0.1:1"))
		if err != nil || string(data) != tc.want {
			t.Errorf("providerFixtureModels(%+v, %s) = %s, %v; want %s", tc.sc, tc.api, data, err, tc.want)
		}
	}
}
