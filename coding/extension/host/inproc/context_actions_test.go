package inproc_test

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestContextActions_ToolScoping verifies that in-process extensions can
// query and modify the active tool set via extension.Context methods.
// This is the integration test for the ContextActions wiring that the
// piglet extension depends on.
func TestContextActions_ToolScoping(t *testing.T) {
	// Track what SetActiveTools receives.
	var setToolsCalled bool
	var setToolsNames []string

	// Simulate a tool registry.
	allTools := []extension.ToolInfo{
		{Name: "read", Description: "Read a file", SourceInfo: "builtin"},
		{Name: "write", Description: "Write a file", SourceInfo: "builtin"},
		{Name: "bash", Description: "Execute bash", SourceInfo: "builtin"},
		{Name: "dispatch_subagent", Description: "Dispatch", SourceInfo: "subagent"},
		{Name: "web_search", Description: "Search", SourceInfo: "web-search"},
	}
	activeTools := []string{"read", "write", "bash", "dispatch_subagent", "web_search"}

	// Create an extension that exercises all scoping APIs.
	var gotAllTools []extension.ToolInfo
	var gotActiveTools []string
	var gotFlagValue any

	scopeExt := extension.Extension{
		Name:         "scope-test",
		Path:         "test:scope",
		ResolvedPath: "test:scope",
		Handlers: map[string][]extension.HandlerFn{
			"session_start": {
				func(args ...any) (any, error) {
					if len(args) < 2 {
						t.Fatal("session_start handler: expected 2 args")
					}
					ctx, ok := args[1].(context.Context)
					if !ok {
						t.Fatal("session_start handler: arg[1] is not context.Context")
					}
					extCtx := extension.FromContext(ctx)
					if extCtx == nil {
						t.Fatal("session_start handler: no extension context")
					}

					// Exercise GetAllTools
					gotAllTools = extCtx.GetAllTools()

					// Exercise GetActiveTools
					gotActiveTools = extCtx.GetActiveTools()

					// Exercise GetFlagValue
					gotFlagValue = extCtx.GetFlagValue("piglet")

					// Exercise SetActiveTools: filter to just builtins
					extCtx.SetActiveTools([]string{"read", "write", "bash"})

					return nil, nil
				},
			},
		},
		Tools:            map[string]extension.RegisteredTool{},
		Commands:         map[string]extension.RegisteredCommand{},
		Flags:            map[string]extension.ExtensionFlag{},
		Shortcuts:        map[extension.KeyID]extension.ExtensionShortcut{},
		MessageRenderers: map[string]extension.MessageRenderer{},
	}

	runner := inproc.NewRunner([]extension.Extension{scopeExt}, t.TempDir())

	// Wire ContextActions: this is what wireInprocContextActions does.
	runner.BindCore(
		extension.ExtensionActions{},
		extension.ContextActions{
			GetAllTools: func() []extension.ToolInfo {
				return allTools
			},
			GetActiveTools: func() []string {
				return activeTools
			},
			SetActiveTools: func(names []string) {
				setToolsCalled = true
				setToolsNames = names
			},
			GetFlagValue: func(name string) any {
				if name == "piglet" {
					return "meta"
				}
				return nil
			},
		},
		nil,
	)

	event := extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	_, err := runner.Emit(context.Background(), event)
	if err != nil {
		t.Fatalf("Emit session_start: %v", err)
	}

	// Verify GetAllTools returned the full set.
	if len(gotAllTools) != 5 {
		t.Errorf("GetAllTools returned %d tools, want 5", len(gotAllTools))
	}
	for i, tool := range gotAllTools {
		if tool.Name != allTools[i].Name {
			t.Errorf("GetAllTools[%d].Name = %q, want %q", i, tool.Name, allTools[i].Name)
		}
	}

	// Verify GetActiveTools returned the active set.
	if len(gotActiveTools) != 5 {
		t.Errorf("GetActiveTools returned %d tools, want 5", len(gotActiveTools))
	}

	// Verify GetFlagValue returned the flag.
	if gotFlagValue != "meta" {
		t.Errorf("GetFlagValue(piglet) = %v, want 'meta'", gotFlagValue)
	}

	// Verify SetActiveTools was called with the filtered set.
	if !setToolsCalled {
		t.Fatal("SetActiveTools was never called")
	}
	if !slices.Equal(setToolsNames, []string{"read", "write", "bash"}) {
		t.Errorf("SetActiveTools received %v, want [read write bash]", setToolsNames)
	}
}

// TestContextActions_NilSafe verifies that Context methods are safe to call
// when ContextActions fields are nil (zero value / unbound).
func TestContextActions_NilSafe(t *testing.T) {
	noopExt := extension.Extension{
		Name:         "noop",
		Path:         "test:noop",
		ResolvedPath: "test:noop",
		Handlers: map[string][]extension.HandlerFn{
			"session_start": {
				func(args ...any) (any, error) {
					if len(args) < 2 {
						return nil, nil
					}
					ctx, _ := args[1].(context.Context)
					if ctx == nil {
						return nil, nil
					}
					extCtx := extension.FromContext(ctx)
					if extCtx == nil {
						return nil, nil
					}

					// All of these must be nil-safe (no panic).
					tools := extCtx.GetAllTools()
					if tools != nil {
						t.Errorf("GetAllTools on unbound context = %v, want nil", tools)
					}
					active := extCtx.GetActiveTools()
					if active != nil {
						t.Errorf("GetActiveTools on unbound context = %v, want nil", active)
					}
					extCtx.SetActiveTools([]string{"read"}) // no-op, must not panic
					val := extCtx.GetFlagValue("anything")
					if val != nil {
						t.Errorf("GetFlagValue on unbound context = %v, want nil", val)
					}
					return nil, nil
				},
			},
		},
		Tools:            map[string]extension.RegisteredTool{},
		Commands:         map[string]extension.RegisteredCommand{},
		Flags:            map[string]extension.ExtensionFlag{},
		Shortcuts:        map[extension.KeyID]extension.ExtensionShortcut{},
		MessageRenderers: map[string]extension.MessageRenderer{},
	}

	// Do NOT call BindCore: ContextActions stays zero.
	runner := inproc.NewRunner([]extension.Extension{noopExt}, t.TempDir())

	event := extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	_, err := runner.Emit(context.Background(), event)
	if err != nil {
		t.Fatalf("Emit session_start: %v", err)
	}
}

// TestContextActions_PigletScopeIntegration simulates the piglet extension's
// actual scoping flow: GetAllTools → ScopeTools → SetActiveTools.
// This is the end-to-end test that catches the bug where ContextActions
// were never wired, making piglet scoping silently no-op.
func TestContextActions_PigletScopeIntegration(t *testing.T) {
	allTools := []extension.ToolInfo{
		{Name: "read", SourceInfo: "builtin"},
		{Name: "bash", SourceInfo: "builtin"},
		{Name: "dispatch_subagent", SourceInfo: "subagent"},
		{Name: "web_search", SourceInfo: "web-search"},
		{Name: "secret_tool", SourceInfo: "evil-ext"},
	}

	var finalActiveTools []string

	// Simulate what the piglet extension does: get all tools, filter by
	// allowed extensions (only builtin + subagent), set active.
	pigletExt := extension.Extension{
		Name:         "piglet-sim",
		Path:         "test:piglet",
		ResolvedPath: "test:piglet",
		Handlers: map[string][]extension.HandlerFn{
			"session_start": {
				func(args ...any) (any, error) {
					ctx, _ := args[1].(context.Context)
					extCtx := extension.FromContext(ctx)
					if extCtx == nil {
						t.Fatal("no extension context in session_start")
					}

					tools := extCtx.GetAllTools()
					if len(tools) == 0 {
						t.Fatal("GetAllTools returned empty: ContextActions not wired")
					}

					// Filter: only allow builtin and subagent sources.
					allowedSources := map[string]bool{
						"builtin":  true,
						"subagent": true,
					}
					var active []string
					for _, tool := range tools {
						src, _ := tool.SourceInfo.(string)
						if allowedSources[src] {
							active = append(active, tool.Name)
						}
					}

					extCtx.SetActiveTools(active)
					return nil, nil
				},
			},
		},
		Tools:            map[string]extension.RegisteredTool{},
		Commands:         map[string]extension.RegisteredCommand{},
		Flags:            map[string]extension.ExtensionFlag{},
		Shortcuts:        map[extension.KeyID]extension.ExtensionShortcut{},
		MessageRenderers: map[string]extension.MessageRenderer{},
	}

	runner := inproc.NewRunner([]extension.Extension{pigletExt}, t.TempDir())
	runner.BindCore(
		extension.ExtensionActions{},
		extension.ContextActions{
			GetAllTools: func() []extension.ToolInfo {
				return allTools
			},
			GetActiveTools: func() []string {
				return []string{"read", "bash", "dispatch_subagent", "web_search", "secret_tool"}
			},
			SetActiveTools: func(names []string) {
				finalActiveTools = names
			},
		},
		nil,
	)

	event := extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	_, err := runner.Emit(context.Background(), event)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Verify scoping worked: only builtin + subagent tools should remain.
	want := []string{"read", "bash", "dispatch_subagent"}
	if !slices.Equal(finalActiveTools, want) {
		t.Errorf("SetActiveTools received %v, want %v", finalActiveTools, want)
	}

	// Verify evil-ext's tool was excluded.
	if slices.Contains(finalActiveTools, "secret_tool") {
		t.Error("secret_tool should have been excluded by piglet scoping")
	}
	if slices.Contains(finalActiveTools, "web_search") {
		t.Error("web_search (from unlisted extension) should have been excluded")
	}
}
