//go:build !pig_strip_llama_cpp

package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

// RPC lists /llama where the loader placed the built-in llama.cpp extension: llama.cpp is the first entry of builtInExtensions
// (extensions/index.ts:8), so the runner lists /llama with the other extension commands in load order, ahead of a later built-in such
// as /mcp, and the catalog adds no command of its own.
// Ports .upstream/v1.1.0/packages/coding-agent/src/extensions/index.ts:8-14 with src/core/resource-loader.ts:703-735.
func TestRPCCatalogListsBuiltInLlamaCommandInLoadOrder(t *testing.T) {
	builtinInfo := func(name string) codingagent.PiSourceInfo {
		return codingagent.PiSourceInfo{Path: "builtin:" + name, Source: "builtin", Scope: "temporary", Origin: "top-level"}
	}
	resolved := func(name, description string, info codingagent.PiSourceInfo) extension.ResolvedCommand {
		return extension.ResolvedCommand{RegisteredCommand: extension.RegisteredCommand{Name: name, Description: description, SourceInfo: info}, InvocationName: name}
	}
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{
		resolved("other", "", codingagent.PiSourceInfo{Path: "/x/other.ts", Source: "local", Scope: "user", Origin: "top-level"}),
		resolved("llama", "Manage llama.cpp router models", builtinInfo("llama.cpp")),
		resolved("mcp", "Manage MCP servers", builtinInfo("mcp")),
	}}
	var names []string
	for _, command := range (headlessCommandCatalog{runner: runner}).commands() {
		names = append(names, command.Name)
	}
	if want := []string{"other", "llama", "mcp"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want the runner's own order %v with no extra llama entry", names, want)
	}
}

// llamaLoads reports whether the production extension set loads the built-in llama.cpp extension under settings and flags.
func llamaLoads(t *testing.T, cwd, agentDir string, flags Args) bool {
	t.Helper()
	loader := newExtensionSetTestLoader(t, cwd, agentDir, flags, llamaBuiltInEntry(t))
	return slices.Contains(reloadExtensionSet(t, loader, nil).paths(), codingagent.BuiltinPathPrefix+llamaBuiltinName)
}

// startBuiltInLlama loads the built-in llama.cpp extension through the production extension set and binds its registrations to the
// session's model registry, as the session runner does, then makes the cache-only refresh every mode runs at startup
// (agent-session-services.ts `modelRuntime.refresh({ allowNetwork: false })`).
func startBuiltInLlama(t *testing.T, services *coding.AgentSessionServices, dir string) {
	t.Helper()
	loader := newExtensionSetTestLoader(t, dir, dir, Args{}, llamaBuiltInEntry(t))
	reloadExtensionSet(t, loader, nil)
	registry := services.Registry().ModelRegistry
	loader.FactoryRuntime().BindProviderActions(extension.ProviderActions{RegisterNativeProviderCarrier: registry.RegisterNativeProvider}, func(err *extension.ExtensionError) { t.Errorf("registration: %s", err.Error) })
	allowNetwork := false
	registry.RefreshModelRuntime(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: &allowNetwork})
}

// Every mode registers the provider and restores its stored catalog without
// network access, so a cached llama.cpp model resolves at startup.
func TestStartBuiltInLlamaRestoresTheStoredCatalog(t *testing.T) {
	t.Setenv("LLAMA_API_KEY", "")
	t.Setenv("LLAMA_BASE_URL", "http://127.0.0.1:9/v1")
	dir := t.TempDir()
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	raw := []byte(`{"id":"cached","name":"cached","api":"openai-completions","provider":"llama.cpp","baseUrl":"http://127.0.0.1:9/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096,"compat":{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"supportsUsageInStreaming":true,"supportsStrictMode":false,"maxTokensField":"max_tokens"}}`)
	if err := store.Write(context.Background(), llama.LlamaProviderID, ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{raw})}); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	startBuiltInLlama(t, services, dir)
	entry, ok := services.Registry().Resolve(llama.LlamaProviderID, "cached")
	if !ok || entry.BaseURL != "http://127.0.0.1:9/v1" || entry.ContextWindow != 4096 {
		t.Fatalf("cached model = %+v, %v", entry, ok)
	}
	if model := services.ModelRuntime().GetModel(llama.LlamaProviderID, "cached"); model == nil {
		t.Fatal("model runtime does not expose the cached llama.cpp model")
	}
}

// Issue #129, D90: a llama.cpp model whose chat template reads enable_thinking offers every budgeted thinking level, and each level sends its own thinking_budget_tokens. Pi's toPiModel (extensions/llama/provider.ts:113-115) maps only off and medium, and its qwen-chat-template request carries no budget, so Pi offers two levels with nothing between them. llama-server reads a per-request thinking_budget_tokens when no budget is set on its command line.
func TestLlamaReasoningModelOffersBudgetedThinkingLevels(t *testing.T) {
	t.Setenv("LLAMA_API_KEY", "")
	bodies := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 32768}}}})
		case "/props":
			_ = json.NewEncoder(w).Encode(map[string]any{"chat_template": "{% if enable_thinking %}think{% endif %}"})
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies <- body
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("LLAMA_BASE_URL", server.URL)
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	startBuiltInLlama(t, services, dir)
	allowNetwork := true
	if result := services.Registry().RefreshModelRuntime(context.Background(), ai.ModelsRefreshOptions{Providers: []string{llama.LlamaProviderID}, AllowNetwork: &allowNetwork}); result.Errors[llama.LlamaProviderID] != nil {
		t.Fatal(result.Errors[llama.LlamaProviderID])
	}
	model, err := coding.BuildModel(llama.LlamaProviderID+"/qwen", services)
	if err != nil {
		t.Fatal(err)
	}
	if levels, want := ai.GetSupportedThinkingLevels(model), []ai.ModelThinkingLevel{ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh}; !reflect.DeepEqual(levels, want) {
		t.Fatalf("supported thinking levels = %v, want %v", levels, want)
	}
	for _, tc := range []struct {
		level  ai.ModelThinkingLevel
		budget any
		enable bool
	}{
		{ai.ThinkingOff, nil, false},
		{ai.ThinkingMinimal, float64(1024), true},
		{ai.ThinkingLow, float64(2048), true},
		{ai.ThinkingMedium, float64(8192), true},
		{ai.ThinkingHigh, float64(16384), true},
	} {
		stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{Thinking: tc.level.ReasoningOption(), IsReasoning: true})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != ai.StopReasonStop {
			t.Fatalf("%s: result = %+v", tc.level, result)
		}
		body := <-bodies
		kwargs, _ := body["chat_template_kwargs"].(map[string]any)
		if kwargs["enable_thinking"] != tc.enable || body["thinking_budget_tokens"] != tc.budget {
			t.Errorf("%s: chat_template_kwargs = %v, thinking_budget_tokens = %v; want enable_thinking %v and budget %v", tc.level, kwargs, body["thinking_budget_tokens"], tc.enable, tc.budget)
		}
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/extensions/index.ts:7-14 with package-manager-cli.ts:842-853: `pi config`
// resolves every built-in extension of main.ts's extensionFactories, and llama.cpp is the first. Its toggle reaches the provider
// gate. The selector here lists only llama.cpp: the extension-set built-ins (codemode, tool-search) are listed too in a real
// `pi config` and sort before llama.cpp, so toggling the first row would toggle codemode.
func TestConfigListsHostedBuiltinLlamaExtension(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	if names := cliBuiltinExtensionNames(); len(names) == 0 || names[0] != "llama.cpp" {
		t.Fatalf("CLI built-in extension names = %q, want llama.cpp first", names)
	}
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
	selector, err := newScopedConfigSelector(cwd, agentDir, sm, sm, false, true, []string{llamaBuiltinName})
	if err != nil {
		t.Fatal(err)
	}
	if rendered := strings.Join(selector.Render(80), "\n"); !strings.Contains(rendered, "Built-in") || !strings.Contains(rendered, "llama.cpp") {
		t.Fatalf("selector does not list the built-in llama.cpp extension:\n%s", rendered)
	}
	selector.HandleInput(" ")
	if got := sm.GetGlobalSettings().Extensions; !slices.Equal(got, []string{"-builtin:llama.cpp"}) {
		t.Fatalf("after the toggle extensions = %q, want [-builtin:llama.cpp]", got)
	}
	if llamaLoads(t, cwd, agentDir, Args{}) {
		t.Fatal("the llama.cpp provider stays enabled after pig config disabled builtin:llama.cpp")
	}
}

// Ports .upstream/v0.99.1/packages/coding-agent/src/extensions/index.ts:8 with resource-loader.ts:552-573 ("--no-extensions also
// disables built-ins, including the llama.cpp provider", plan section 4.4): the llama.cpp provider is the built-in extension
// `builtin:llama.cpp`.
func TestBuiltinLlamaFollowsBuiltinExtensionSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		global string
		flags  Args
		want   bool
	}{
		{"default", "", Args{}, true},
		{"disabled in settings", `{"extensions":["-builtin:llama.cpp"]}`, Args{}, false},
		{"no extensions", "", Args{NoExtensions: true}, false},
		{"no extensions with explicit path", "", Args{NoExtensions: true, Extensions: []string{"builtin:llama.cpp"}}, true},
		{"no extensions with another explicit built-in", "", Args{NoExtensions: true, Extensions: []string{"builtin:mcp"}}, false},
		{"no extensions with a file extension", "", Args{NoExtensions: true, Extensions: []string{"./x.ts"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cwd, agentDir := resourceExtensionFixture(t)
			if tc.global != "" {
				writeResourceTestFiles(t, root, map[string]string{"agent/settings.json": tc.global})
			}
			if got := llamaLoads(t, cwd, agentDir, tc.flags); got != tc.want {
				t.Fatalf("llama.cpp loads = %t, want %t", got, tc.want)
			}
		})
	}
}
