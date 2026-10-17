package coding

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func runtimeResultSession(t *testing.T, services *AgentSessionServices, options CreateAgentSessionRuntimeOptions) (*Session, LoadExtensionsResult) {
	t.Helper()
	root := t.TempDir()
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{Errors: []ExtensionLoadError{{Path: "ext.ts", Error: "boom"}}}, nil
		},
	})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, Model: fakeModel(), Runner: inproc.NewRunner(nil, services.CWD()), ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, loader.GetExtensions()
}

// agent-session-runtime.ts:23 `interface CreateAgentSessionRuntimeResult extends CreateAgentSessionResult`, and Pi's factory returns the created result with the services and diagnostics added ({ ...created, services,
// diagnostics }, main.ts:839-860). createAgentSessionRuntime constructs the Runtime over the factory's result (agent-session-runtime.ts:426-438), so the Runtime holds the factory's extensionsResult, Session and modelFallbackMessage. The factory's extensionsResult carries a warning the Session's loader does not have, so a Runtime that rebuilt it from the loader fails.
func TestRuntimeKeepsTheCreatedResultMembers(t *testing.T) {
	services := newTestServices(t)
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		session, created := runtimeResultSession(t, services, options)
		created.Warnings = append(created.Warnings, ExtensionLoadWarning{Path: "factory.ts", Warning: "from the factory"})
		return CreateAgentSessionRuntimeResult{Session: session, Services: services, ExtensionsResult: created, ModelFallbackMessage: "Could not restore model a/b. Using c/d"}, nil
	}
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if current := runtime.replacement.current.Load(); len(current.ExtensionsResult.Errors) != 1 || current.ExtensionsResult.Errors[0].Path != "ext.ts" || len(current.ExtensionsResult.Warnings) != 1 || current.ExtensionsResult.Warnings[0].Path != "factory.ts" {
		t.Fatalf("runtime lost the created extensionsResult: %+v", current.ExtensionsResult)
	}
	if got := runtime.ModelFallbackMessage(); got != "Could not restore model a/b. Using c/d" {
		t.Fatalf("model fallback = %q, want the created result's", got)
	}
}

// new AgentSessionRuntime(session, services, ...) over a Session carries that Session's loaded extensions as its result's extensionsResult.
func TestNewAgentSessionRuntimeCarriesTheSessionsExtensionsResult(t *testing.T) {
	services := newTestServices(t)
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	session, _ := runtimeResultSession(t, services, CreateAgentSessionRuntimeOptions{SessionManager: manager})
	factory := func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		return CreateAgentSessionRuntimeResult{}, nil
	}
	runtime, err := NewAgentSessionRuntime(t.Context(), session, services, factory, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	current := runtime.replacement.current.Load()
	if len(current.ExtensionsResult.Errors) != 1 || current.ExtensionsResult.Errors[0].Path != "ext.ts" {
		t.Fatalf("runtime result extensionsResult = %+v, want the session's loaded extensions", current.ExtensionsResult)
	}
}
