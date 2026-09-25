package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
)

// RPC lists and runs /llama like upstream's built-in inline extension
// command: first in get_commands, and in RPC mode it only warns.
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
	runner := &fakeRPCCommandRunner{commands: []extension.ResolvedCommand{{RegisteredCommand: extension.RegisteredCommand{Name: "other"}, InvocationName: "other"}}}
	catalog := rpcCommandCatalog{runner: runner, llama: host, notify: func(message, kind string) {
		notices = append(notices, [2]string{message, kind})
	}}

	commands := catalog.commands()
	want := RPCSlashCommand{
		Name: "llama", Description: "Manage llama.cpp router models", Source: "extension",
		SourceInfo: RPCSourceInfo{Path: "<inline:llama.cpp>", Source: "inline", Scope: "temporary", Origin: "top-level"},
	}
	if len(commands) != 2 || !reflect.DeepEqual(commands[0], want) || commands[1].Name != "other" {
		t.Fatalf("commands = %#v", commands)
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
