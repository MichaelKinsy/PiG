package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestSelectStartupModelCustomModelInheritsBuiltInDefaults pins --model and saved defaultModel
// for models.json custom models under built-in providers that omit api and/or baseUrl
// (provider-composer.ts:212-261, 336-342).
func TestSelectStartupModelCustomModelInheritsBuiltInDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	cases := []struct {
		name       string
		provider   string
		modelID    string
		modelsJSON string
		authJSON   string
		wantAPI    ai.API
		wantBase   string
	}{
		{
			name:       "groq-omits-both",
			provider:   "groq",
			modelID:    "my-custom-groq",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"my-custom-groq","name":"My Custom Groq","contextWindow":64000,"maxTokens":8192}]}}}`,
			authJSON:   `{"groq":{"type":"api_key","key":"gsk-fake"}}`,
			wantAPI:    ai.APIOpenAICompletions,
			wantBase:   "https://api.groq.com/openai/v1",
		},
		{
			name:       "github-copilot-oauth-omits-both",
			provider:   "github-copilot",
			modelID:    "my-custom-copilot",
			modelsJSON: `{"providers":{"github-copilot":{"models":[{"id":"my-custom-copilot","name":"My Custom Copilot"}]}}}`,
			authJSON:   `{"github-copilot":{"type":"oauth","refresh":"r","access":"tid=0;exp=9999999999;","expires":9999999999000}}`,
			wantAPI:    ai.APIOpenAICompletions,
			wantBase:   "https://api.individual.githubcopilot.com",
		},
		{
			name:       "opencode-go-multiapi-explicit-anthropic-omits-baseurl",
			provider:   "opencode-go",
			modelID:    "my-custom-og-anth",
			modelsJSON: `{"providers":{"opencode-go":{"models":[{"id":"my-custom-og-anth","api":"anthropic-messages"}]}}}`,
			authJSON:   `{"opencode-go":{"type":"api_key","key":"oc-fake"}}`,
			wantAPI:    ai.APIAnthropicMessages,
			wantBase:   "https://opencode.ai/inference/go/anthropic",
		},
		{
			name:       "explicit-api-and-baseurl-win",
			provider:   "groq",
			modelID:    "my-explicit-groq",
			modelsJSON: `{"providers":{"groq":{"models":[{"id":"my-explicit-groq","api":"openai-responses","baseUrl":"` + server.URL + `"}]}}}`,
			authJSON:   `{"groq":{"type":"api_key","key":"gsk-fake"}}`,
			wantAPI:    ai.APIOpenAIResponses,
			wantBase:   server.URL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateProviderAuthEnv(t)
			for k, v := range map[string]string{
				"PIG_CODING_AGENT_DIR": dir, "PI_CODING_AGENT_DIR": dir,
				"PIG_HOME": dir, "PI_HOME": dir,
				"PI_OFFLINE": "1", "PIG_OFFLINE": "1",
			} {
				t.Setenv(k, v)
			}
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(tc.modelsJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(tc.authJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			svcs := testServices(t, dir)
			defer svcs.Close()

			// --model path
			byFlag, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: tc.provider + "/" + tc.modelID}, codingagent.Settings{}, svcs)
			if err != nil {
				t.Fatalf("selectStartupModel(--model): %v", err)
			}
			if len(byFlag.Warnings) != 0 {
				t.Errorf("--model warnings = %v, want none", byFlag.Warnings)
			}
			if byFlag.Model == nil || byFlag.Model.ProviderMeta.API != tc.wantAPI || byFlag.Model.ProviderMeta.BaseURL != tc.wantBase {
				t.Errorf("--model got api=%q baseUrl=%q, want api=%q baseUrl=%q", byFlag.Model.ProviderMeta.API, byFlag.Model.ProviderMeta.BaseURL, tc.wantAPI, tc.wantBase)
			}

			// Saved defaultModel path (interactive startup)
			byDefault, err := selectStartupModel(context.Background(), startupModelOptions{}, codingagent.Settings{DefaultProvider: tc.provider, DefaultModel: tc.modelID}, svcs)
			if err != nil {
				t.Fatalf("selectStartupModel(default): %v", err)
			}
			if byDefault.Model == nil || byDefault.Model.ProviderMeta.API != tc.wantAPI || byDefault.Model.ProviderMeta.BaseURL != tc.wantBase {
				t.Errorf("defaultModel got api=%q baseUrl=%q, want api=%q baseUrl=%q", byDefault.Model.ProviderMeta.API, byDefault.Model.ProviderMeta.BaseURL, tc.wantAPI, tc.wantBase)
			}
		})
	}
}

// TestPrintModeCustomModelWithoutAPIReachesLocalEndpointAndPreservesMetadata drives the real CLI
// --model path through print mode and RPC get_state against a local loopback server.
func TestPrintModeCustomModelWithoutAPIReachesLocalEndpointAndPreservesMetadata(t *testing.T) {
	var mu sync.Mutex
	var gotPaths []string
	var gotAuths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPaths = append(gotPaths, r.URL.Path)
		gotAuths = append(gotAuths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"LOCAL_PONG\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	dir := isolateProviderAuthEnv(t)
	for k, v := range map[string]string{
		"PIG_CODING_AGENT_DIR": dir, "PI_CODING_AGENT_DIR": dir,
		"PIG_HOME": dir, "PI_HOME": dir,
		"PI_OFFLINE": "1", "PIG_OFFLINE": "1",
	} {
		t.Setenv(k, v)
	}
	modelsJSON := `{"providers":{"groq":{"baseUrl":"` + server.URL + `","models":[{"id":"my-local-groq","name":"My Local Groq","contextWindow":64000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"groq":{"type":"api_key","key":"gsk-local-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Drive real CLI startup build + print mode session with --model groq/my-local-groq -p hi
	svcs := testServices(t, dir)
	defer svcs.Close()
	selected, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: "groq/my-local-groq"}, codingagent.Settings{}, svcs)
	if err != nil {
		t.Fatalf("selectStartupModel: %v", err)
	}
	if selected.Model == nil {
		t.Fatal("selected.Model == nil")
	}
	if selected.Model.ProviderMeta.API != ai.APIOpenAICompletions || selected.Model.ProviderMeta.BaseURL != server.URL {
		t.Fatalf("selected model api=%q baseUrl=%q, want openai-completions / %s", selected.Model.ProviderMeta.API, selected.Model.ProviderMeta.BaseURL, server.URL)
	}
	// Verify RPC wire serialization (the exact object RPC get_state and print/json session expose)
	rpcModel := rpcModelValue(selected.Model)
	encoded, err := json.Marshal(rpcModel)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"api":"openai-completions"`)) || !bytes.Contains(encoded, []byte(`"baseUrl":"`+server.URL+`"`)) {
		t.Errorf("rpcModelInfo JSON = %s, want api openai-completions and baseUrl %s", encoded, server.URL)
	}

	// Stream directly through the built model provider (no runtime GetModel rescue) to verify the constructed provider itself carries the inherited API and base URL
	transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}})
	stream, err := selected.Model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{MaxRetries: new(0)})
	if err != nil {
		t.Fatalf("selected.Model.Provider.Stream: %v", err)
	}
	msg := stream.Result()
	if msg == nil || msg.StopReason == ai.StopReasonError {
		t.Fatalf("stream result = %+v", msg)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotPaths) == 0 || !strings.HasSuffix(gotPaths[0], "/chat/completions") {
		t.Errorf("server paths = %v, want /chat/completions", gotPaths)
	}
	if len(gotAuths) == 0 || gotAuths[0] != "Bearer gsk-local-key" {
		t.Errorf("server auths = %v, want Bearer gsk-local-key", gotAuths)
	}
}

// TestPrintModeEndToEndCustomModelWithoutAPIUsesInheritedDefaults drives the real CLI runtime build
// and print mode (runPrintMode) with --model groq/my-print-groq where models.json omits api (and sets a local
// provider baseUrl so the request stays hermetic), plus a bundled model control.
func TestPrintModeEndToEndCustomModelWithoutAPIUsesInheritedDefaults(t *testing.T) {
	var mu sync.Mutex
	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPaths = append(gotPaths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"PRINT_MODE_OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	dir := isolateProviderAuthEnv(t)
	for k, v := range map[string]string{
		"PIG_CODING_AGENT_DIR": dir, "PI_CODING_AGENT_DIR": dir,
		"PIG_HOME": dir, "PI_HOME": dir,
		"PI_OFFLINE": "1", "PIG_OFFLINE": "1",
	} {
		t.Setenv(k, v)
	}
	modelsJSON := `{"providers":{"groq":{"baseUrl":"` + server.URL + `","models":[{"id":"my-print-groq","name":"My Print Groq","contextWindow":64000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"groq":{"type":"api_key","key":"gsk-print-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	svcs := testServices(t, dir)
	defer svcs.Close()
	selected, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: "groq/my-print-groq"}, codingagent.Settings{}, svcs)
	if err != nil {
		t.Fatalf("selectStartupModel: %v", err)
	}
	if selected.Model == nil || selected.Model.ProviderMeta.API != ai.APIOpenAICompletions || selected.Model.ProviderMeta.BaseURL != server.URL {
		t.Fatalf("selected model = %+v, want openai-completions / %s", selected.Model, server.URL)
	}

	host := printModeRuntime{
		Services: svcs,
		Session: coding.SessionStartOptions{
			Model:            selected.Model,
			NoSession:        true,
			SkipBuiltinTools: true,
			ResourceLoader:   newCLIResourceLoader(svcs.CWD(), svcs.AgentDir()),
		},
	}
	result := runPrintModeForTest(t, host, printModeOptions{
		Mode:           "text",
		InitialMessage: "hi",
	})
	if result.err != nil || result.stderr != "" {
		t.Fatalf("runPrintModeForTest err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stdout, "PRINT_MODE_OK") {
		t.Errorf("print stdout = %q, want PRINT_MODE_OK", result.stdout)
	}
	mu.Lock()
	paths := append([]string(nil), gotPaths...)
	mu.Unlock()
	if len(paths) == 0 || !strings.HasSuffix(paths[0], "/chat/completions") {
		t.Errorf("server paths = %v, want /chat/completions", paths)
	}

	// Control: bundled model on the same provider keeps its own catalog values with the provider baseUrl override.
	bundled, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: "groq/openai/gpt-oss-120b"}, codingagent.Settings{}, svcs)
	if err != nil {
		t.Fatalf("selectStartupModel(bundled): %v", err)
	}
	if bundled.Model == nil || bundled.Model.ProviderMeta.API != ai.APIOpenAICompletions || bundled.Model.ProviderMeta.BaseURL != server.URL {
		t.Errorf("bundled control api=%q baseUrl=%q, want openai-completions / %s", bundled.Model.ProviderMeta.API, bundled.Model.ProviderMeta.BaseURL, server.URL)
	}
}
