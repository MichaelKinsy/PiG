package llama

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// recordingUI records what the /llama handler asks of ctx.ui.
type recordingUI struct {
	extension.UIContext
	mu      sync.Mutex
	notices []notification
	customs int
}

func (u *recordingUI) Notify(message, kind string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.notices = append(u.notices, notification{message, kind})
}

func (u *recordingUI) Custom(context.Context, extension.CustomFactory, *extension.CustomOptions) (any, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.customs++
	return nil, nil
}

// sessionRegistry is the ctx.modelRegistry of a session: Pi's ModelRegistry with the two members /llama reads served by Models.
type sessionRegistry struct {
	extension.ModelRegistry
	testRegistry
}

func (r sessionRegistry) GetProviderAuth(ctx context.Context, id string) (*ai.AuthResult, error) {
	return r.testRegistry.GetProviderAuth(ctx, id)
}

func (r sessionRegistry) Refresh(ctx context.Context, options ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return r.testRegistry.Refresh(ctx, options)
}

func commandCtx(ui extension.UIContext, mode extension.ExtensionMode, registry extension.ModelRegistry) context.Context {
	base := extension.NewContext("/work", ui, func() error { return nil }, extension.ContextActions{
		GetMode: func() extension.ExtensionMode { return mode }, ModelRegistry: registry,
	})
	return extension.WithCommandContext(context.Background(), extension.NewCommandContext(base, extension.CommandActions{}))
}

// The /llama handler runs index.ts's command with the invocation's ctx.ui and ctx.modelRegistry: it warns outside interactive mode,
// asks for /login without a configured server, and otherwise shows the manager through ctx.ui.custom.
// Ports packages/coding-agent/src/extensions/llama/index.ts (registerCommand handler, configuredClient).
func TestCommandHandlerRunsWithTheInvocationsUIAndModelRegistry(t *testing.T) {
	clearLlamaEnv(t)
	server := llamaServer(t, map[string]any{"id": "alpha", "status": map[string]any{"value": "loaded"}})
	models, controller, credentials := newTestModels(t, t.TempDir())
	handler := CommandHandler(controller)

	ui := &recordingUI{UIContext: extension.NoopUIContext}
	if err := handler(commandCtx(ui, extension.ModeRPC, sessionRegistry{testRegistry: testRegistry{models}}), ""); err != nil {
		t.Fatal(err)
	}
	if err := handler(commandCtx(ui, extension.ModeTUI, sessionRegistry{testRegistry: testRegistry{models}}), ""); err != nil {
		t.Fatal(err)
	}
	want := []notification{
		{"/llama is available in interactive mode", "warning"},
		{"Configure llama.cpp with /login llama.cpp", "warning"},
	}
	if !reflect.DeepEqual(ui.notices, want) || ui.customs != 0 {
		t.Fatalf("notices = %+v, custom views = %d", ui.notices, ui.customs)
	}

	if err := credentials.Set(LlamaProviderID, ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": server.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := handler(commandCtx(ui, extension.ModeTUI, sessionRegistry{testRegistry: testRegistry{models}}), ""); err != nil {
		t.Fatal(err)
	}
	if ui.customs != 1 {
		t.Fatalf("custom views = %d, want the manager shown through ctx.ui.custom", ui.customs)
	}

	if err := handler(commandCtx(ui, extension.ModeTUI, nil), ""); err == nil {
		t.Fatal("a runtime without a model registry did not fail the command")
	}
}
