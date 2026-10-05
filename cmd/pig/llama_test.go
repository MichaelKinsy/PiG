package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

// RPC lists /llama where the loader placed the built-in llama.cpp extension, and runs it like upstream's built-in extension
// command: llama.cpp is the first entry of builtInExtensions (extensions/index.ts:8), so the runner lists /llama with the other
// extension commands in load order, ahead of a later built-in such as /mcp, and the catalog adds no command of its own. In RPC
// mode /llama only warns.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:8-14 with src/core/resource-loader.ts:703-735 and
// src/modes/rpc/rpc-mode.ts:683.
func TestRPCCatalogListsAndRunsBuiltInLlamaCommand(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	dir := t.TempDir()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	host := startBuiltInLlama(context.Background(), services)
	var notices [][2]string
	builtinInfo := func(name string) codingagent.PiSourceInfo {
		path := "builtin:" + name
		return codingagent.PiSourceInfo{Path: path, Source: "builtin", Scope: "temporary", Origin: "top-level"}
	}
	resolved := func(name, description string, info codingagent.PiSourceInfo) extension.ResolvedCommand {
		return extension.ResolvedCommand{RegisteredCommand: extension.RegisteredCommand{Name: name, Description: description, SourceInfo: info}, InvocationName: name}
	}
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{
		resolved("other", "", codingagent.PiSourceInfo{Path: "/x/other.ts", Source: "local", Scope: "user", Origin: "top-level"}),
		resolved("llama", "Manage llama.cpp router models", builtinInfo("llama.cpp")),
		resolved("mcp", "Manage MCP servers", builtinInfo("mcp")),
	}}
	catalog := headlessCommandCatalog{runner: runner, llama: host, notify: func(message, kind string) {
		notices = append(notices, [2]string{message, kind})
	}}

	var names []string
	for _, command := range catalog.commands() {
		names = append(names, command.Name)
	}
	if want := []string{"other", "llama", "mcp"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("commands = %v, want the runner's own order %v with no extra llama entry", names, want)
	}
	if expanded, handled := catalog.routePrompt(context.Background(), "/llama now"); !handled || expanded != "" {
		t.Fatalf("routePrompt(/llama) = %q, %v", expanded, handled)
	}
	if want := [][2]string{{"/llama is available in interactive mode", "warning"}}; !reflect.DeepEqual(notices, want) {
		t.Fatalf("notices = %v", notices)
	}
	if len(runner.executed) != 0 {
		t.Fatalf("runner executed %v", runner.executed)
	}
}

// Every mode registers the provider and restores its stored catalog without
// network access, so a cached llama.cpp model resolves at startup.
func TestStartBuiltInLlamaRestoresTheStoredCatalog(t *testing.T) {
	t.Setenv("LLAMA_API_KEY", "")
	t.Setenv("LLAMA_BASE_URL", "http://127.0.0.1:9/v1")
	dir := t.TempDir()
	store := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json"))
	raw := []byte(`{"id":"cached","name":"cached","api":"openai-completions","provider":"llama.cpp","baseUrl":"http://127.0.0.1:9/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":4096,"maxTokens":4096,"compat":{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"supportsUsageInStreaming":true,"supportsStrictMode":false,"maxTokensField":"max_tokens"}}`)
	if err := store.Write(context.Background(), llama.LlamaProviderID, ai.ModelsStoreEntry{Models: []json.RawMessage{raw}}); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	startBuiltInLlama(context.Background(), services)
	entry, ok := services.Registry().Resolve(llama.LlamaProviderID, "cached")
	if !ok || entry.APIKey != "local" || entry.BaseURL != "http://127.0.0.1:9/v1" || entry.ContextWindow != 4096 {
		t.Fatalf("cached model = %+v, %v", entry, ok)
	}
	if model := services.ModelRuntime().GetModel(llama.LlamaProviderID, "cached"); model == nil {
		t.Fatal("model runtime does not expose the cached llama.cpp model")
	}
}

// Only the built-in llama.cpp extension's command runs the llama host. Upstream each registered command keeps its own handler
// (core/extensions/runner.ts resolveRegisteredCommands), so a file extension that also registers /llama runs its own handler
// as llama:1 and the built-in one stays llama:2 (Pi 0.99.2 RPC probe: `/llama:1` notifies the file extension's message,
// `/llama:2` warns "/llama is available in interactive mode").
// Ports .upstream/v0.99.2/packages/coding-agent/src/core/extensions/runner.ts (resolveRegisteredCommands) with
// src/extensions/llama/index.ts (registerCommand "llama").
func TestRPCCatalogRunsAnotherExtensionsLlamaCommandThroughTheRunner(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	dir := t.TempDir()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	host := startBuiltInLlama(context.Background(), services)
	var notices [][2]string
	resolved := func(invocation, path, source string) extension.ResolvedCommand {
		info := codingagent.PiSourceInfo{Path: path, Source: source, Scope: "user", Origin: "top-level"}
		return extension.ResolvedCommand{RegisteredCommand: extension.RegisteredCommand{Name: "llama", SourceInfo: info}, InvocationName: invocation}
	}
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{
		resolved("llama:1", "/x/other.ts", "auto"),
		resolved("llama:2", codingagent.LlamaExtensionPath, "builtin"),
	}}
	catalog := headlessCommandCatalog{runner: runner, mode: "rpc", llama: host, notify: func(message, kind string) {
		notices = append(notices, [2]string{message, kind})
	}}
	if _, handled := catalog.routePrompt(context.Background(), "/llama:1 now"); !handled {
		t.Fatal("/llama:1 was not handled")
	}
	if want := []string{"llama:1 now"}; !reflect.DeepEqual(runner.executed, want) || len(notices) != 0 {
		t.Fatalf("/llama:1: runner executed %v, notices %v; want the file extension's handler only", runner.executed, notices)
	}
	if _, handled := catalog.routePrompt(context.Background(), "/llama:2"); !handled {
		t.Fatal("/llama:2 was not handled")
	}
	if want := [][2]string{{"/llama is available in interactive mode", "warning"}}; !reflect.DeepEqual(notices, want) || len(runner.executed) != 1 {
		t.Fatalf("/llama:2: notices %v, runner executed %v; want the llama host only", notices, runner.executed)
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
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	host := startBuiltInLlama(context.Background(), services)
	if result := host.Refresh(context.Background(), true); result.Err != nil {
		t.Fatal(result.Err)
	}
	model, err := coding.BuildModel(llama.LlamaProviderID+"/qwen", services)
	if err != nil {
		t.Fatal(err)
	}
	if levels, want := ai.GetSupportedThinkingLevels(model), []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingMinimal, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh}; !reflect.DeepEqual(levels, want) {
		t.Fatalf("supported thinking levels = %v, want %v", levels, want)
	}
	for _, tc := range []struct {
		level  ai.ThinkingLevel
		budget any
		enable bool
	}{
		{ai.ThinkingOff, nil, false},
		{ai.ThinkingMinimal, float64(1024), true},
		{ai.ThinkingLow, float64(2048), true},
		{ai.ThinkingMedium, float64(8192), true},
		{ai.ThinkingHigh, float64(16384), true},
	} {
		stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{Thinking: tc.level, IsReasoning: true})
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
