package main

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestDefaultModelPerProviderMatchesPinnedUpstream derives its expectation from
// the pinned Pi model-resolver.ts defaultModelPerProvider table.
func TestDefaultModelPerProviderMatchesPinnedUpstream(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", ".upstream", "current", "packages", "coding-agent", "src", "core", "model-resolver.ts"))
	if err != nil {
		t.Fatalf("read pinned Pi model-resolver.ts (run make upstream-mirror): %v", err)
	}
	text := string(source)
	start := strings.Index(text, "export const defaultModelPerProvider")
	if start < 0 {
		t.Fatal("defaultModelPerProvider not found in pinned Pi model-resolver.ts")
	}
	end := strings.Index(text[start:], "};")
	if end < 0 {
		t.Fatal("unterminated defaultModelPerProvider in pinned Pi model-resolver.ts")
	}
	want := map[string]string{}
	var wantOrder []string
	entry := regexp.MustCompile(`(?m)^\t"?([A-Za-z0-9-]+)"?: "([^"]*)",$`)
	for _, match := range entry.FindAllStringSubmatch(text[start:start+end], -1) {
		want[match[1]] = match[2]
		wantOrder = append(wantOrder, match[1])
	}
	var gotOrder []string
	for _, entry := range defaultModelPerProviderOrder {
		gotOrder = append(gotOrder, entry.provider)
	}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Fatalf("defaultModelPerProviderOrder providers = %v\nwant pinned Pi declaration order %v", gotOrder, wantOrder)
	}
	if len(want) == 0 {
		t.Fatal("no defaultModelPerProvider entries parsed from pinned Pi model-resolver.ts")
	}
	if got := defaultModelPerProvider(); !maps.Equal(got, want) {
		t.Fatalf("defaultModelPerProvider() = %v\nwant pinned Pi table %v", got, want)
	}
}

func TestResolveModelUsesFirstAvailableCustomModel(t *testing.T) {
	dir := t.TempDir()
	models := `{"providers":{"fixture":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"fixture-key","api":"openai-completions","models":[{"id":"model-two","name":"Model Two","reasoning":true},{"id":"model-one","name":"Model One"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	previousAgentDir := agentDirForModelOverride
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = previousAgentDir })
	model, _, warning, err := resolveModel("", "", codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if model == nil || model.Provider.ID() != "fixture" || model.ID != "model-two" {
		t.Fatalf("model = %#v, want fixture/model-two", model)
	}
	if model.DisplayName != "Model Two" {
		t.Fatalf("model.DisplayName = %q, want %q", model.DisplayName, "Model Two")
	}
	if model.Capabilities.MaxThinking != ai.ThinkingHigh {
		t.Fatalf("MaxThinking = %q, want high", model.Capabilities.MaxThinking)
	}
	if warning != "" {
		t.Fatalf("warning = %q", warning)
	}
}

// TestResolveModelProviderFlagWithoutModelIsIgnored mirrors upstream
// main.ts, which resolves --provider only together with --model: the
// initial model comes from findInitialModel.
func TestResolveModelProviderFlagWithoutModelIsIgnored(t *testing.T) {
	dir := isolateProviderAuthEnv(t)
	t.Setenv("TOGETHER_API_KEY", "sk-together")
	model, _, _, err := resolveModel("", "anthropic", codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if model == nil || model.ProviderMeta.ProviderID != "together" || model.ID != "moonshotai/Kimi-K2.6" {
		t.Fatalf("model = %+v, want the together default", model)
	}
	if got := model.ProviderMeta.BaseURL; got != "https://api.together.ai/v1" {
		t.Fatalf("baseURL = %q, want %q", got, "https://api.together.ai/v1")
	}
}

// TestResolveModel_ThreadsProviderEnvIntoProvider proves the factory wiring:
// resolveModel sets the built provider's cfg.Env from the resolved ModelEntry's
// provider-scoped env (auth.json env), so a scoped PI_CACHE_RETENTION="none"
// suppresses the OpenAI prompt_cache_key even though the process env sets
// "short". Drives the production path cmd/pig/model.go buildProvider → cfg.Env.
func TestResolveModel_ThreadsProviderEnvIntoProvider(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	models := `{"providers":{"myco":{"baseUrl":"` + srv.URL + `","apiKey":"sk-x","api":"openai-completions",` +
		`"compat":{"sendSessionAffinityHeaders":true},` +
		`"models":[{"id":"m1","name":"M1"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"myco":{"type":"api_key","key":"sk-x","env":{"PI_CACHE_RETENTION":"none"}}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(authJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	previousAgentDir := agentDirForModelOverride
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = previousAgentDir })

	// Process env would enable prompt caching; the scoped "none" must win.
	t.Setenv("PI_CACHE_RETENTION", "short")

	model, _, _, err := resolveModel("myco/m1", "", codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	stream, err := model.Provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}},
	}), ai.StreamOptions{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = stream.Result()
	if _, ok := body["prompt_cache_key"]; ok {
		t.Fatalf("scoped PI_CACHE_RETENTION=none must omit prompt_cache_key, got %v", body["prompt_cache_key"])
	}
}

// TestResolveModel_ThreadsAzureScopedEnv proves cmd/pig wires entry.Env into the
// Azure provider config: a scoped AZURE_OPENAI_API_VERSION from auth.json must win
// over the process env and land in the request's api-version query.
func TestResolveModel_ThreadsAzureScopedEnv(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	models := `{"providers":{"azure-openai-responses":{"baseUrl":"` + srv.URL + `","apiKey":"k",` +
		`"api":"azure-openai-responses","models":[{"id":"m1","name":"M1"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"azure-openai-responses":{"type":"api_key","key":"k","env":{"AZURE_OPENAI_API_VERSION":"scoped-ver"}}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(authJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	previousAgentDir := agentDirForModelOverride
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = previousAgentDir })

	// Process env would set a different api-version; the scoped value must win.
	t.Setenv("AZURE_OPENAI_API_VERSION", "process-ver")
	t.Setenv("AZURE_OPENAI_API_KEY", "k")

	model, _, _, err := resolveModel("azure-openai-responses/m1", "", codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	stream, err := model.Provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}},
	}), ai.StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = stream.Result()
	if gotQuery != "api-version=scoped-ver" {
		t.Fatalf("request query = %q, want api-version=scoped-ver (scoped env must win over process)", gotQuery)
	}
}

// TestResolveModel_ThreadsAnthropicScopedEnv proves the startup resolver wires
// entry.Env into the Anthropic provider config: a scoped PI_CACHE_RETENTION=none
// from auth.json must win over the process env and suppress the
// x-session-affinity header. The CLI's former builder passed entry.Env directly;
// the shared coding.BuildModelFromEntry must keep doing so.
func TestResolveModel_ThreadsAnthropicScopedEnv(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-session-affinity")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	models := `{"providers":{"myco-anthropic":{"baseUrl":"` + srv.URL + `","apiKey":"sk-x",` +
		`"api":"anthropic-messages",` +
		`"compat":{"sendSessionAffinityHeaders":true},` +
		`"models":[{"id":"m1","name":"M1"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"myco-anthropic":{"type":"api_key","key":"sk-x","env":{"PI_CACHE_RETENTION":"none"}}}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(authJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Process env would send the affinity header; the scoped "none" must win.
	t.Setenv("PI_CACHE_RETENTION", "short")

	model, _, _, err := resolveModel("myco-anthropic/m1", "", codingagent.Settings{}, testServices(t, dir))
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	stream, err := model.Provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}},
	}), ai.StreamOptions{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = stream.Result()
	if gotHeader != "" {
		t.Fatalf("scoped PI_CACHE_RETENTION=none must omit x-session-affinity, got %q", gotHeader)
	}
}
