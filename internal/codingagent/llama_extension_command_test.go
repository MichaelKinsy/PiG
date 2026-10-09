//go:build !pig_strip_llama_cpp

package codingagent

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/tui"
)

// llamaExtension is the built-in llama.cpp extension as the loader hands it to the runner: path `builtin:llama.cpp`, hidden, with
// the one /llama command (extensions/llama/index.ts registerCommand).
func llamaExtension() extension.Extension {
	info := PiSourceInfo{Path: LlamaExtensionPath, Source: "builtin", Scope: "temporary", Origin: "top-level"}
	return extension.Extension{
		Name: "llama.cpp", Path: LlamaExtensionPath, ResolvedPath: LlamaExtensionPath, Hidden: true, SourceInfo: info,
		CommandOrder: []string{llama.CommandName},
		Commands: map[string]extension.RegisteredCommand{
			llama.CommandName: {Name: llama.CommandName, Description: llama.CommandDescription, SourceInfo: info},
		},
	}
}

func popupNames(t *testing.T, m *InteractiveMode) []string {
	t.Helper()
	suggestions := m.buildAutocompleteProvider().GetSuggestions(context.Background(), []string{"/"}, 0, 1, tui.AutocompleteSuggestionOptions{})
	if suggestions == nil {
		t.Fatal("no suggestions for /")
	}
	var names []string
	for _, item := range suggestions.Items {
		names = append(names, item.Value)
	}
	return names
}

// Pi 0.99.2 `pi --no-extensions` opens the slash popup as `(1/24)`: BUILTIN_SLASH_COMMANDS has 24 entries
// (core/slash-commands.ts:19-44) and createBaseAutocompleteProvider adds only prompt templates, extension commands and skills
// (interactive-mode.ts:691-775). /llama is an extension command, so it is absent while the llama.cpp extension is not loaded.
func TestSlashPopupWithoutExtensionsListsPisTwentyFourBuiltins(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.opts.AgentDir = t.TempDir()
	names := popupNames(t, m)
	if len(names) != 24 {
		t.Fatalf("popup entries = %d %q, want Pi's 24 built-in commands", len(names), names)
	}
	if names[0] != "settings" || names[len(names)-1] != "quit" {
		t.Fatalf("popup = %q, want settings first and quit last", names)
	}
}

// With the built-in llama.cpp extension loaded, /llama comes from the runner's commands, after the built-ins and prompt templates
// (interactive-mode.ts:739-775), and before a later built-in extension's /mcp.
func TestSlashPopupListsLlamaAmongExtensionCommandsInLoadOrder(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.opts.AgentDir = t.TempDir()
	mcp := extension.Extension{Path: "builtin:mcp", CommandOrder: []string{"mcp"}, Commands: map[string]extension.RegisteredCommand{"mcp": {Name: "mcp", Description: "Manage MCP servers"}}}
	m.newRunner = inproc.NewRunner([]extension.Extension{llamaExtension(), mcp}, m.opts.AgentDir)
	t.Cleanup(func() { m.newRunner.Invalidate("") })
	names := popupNames(t, m)
	if len(names) != 26 {
		t.Fatalf("popup entries = %d %q, want 24 built-ins plus /llama and /mcp", len(names), names)
	}
	if got := names[24:]; got[0] != "llama" || got[1] != "mcp" {
		t.Fatalf("extension commands = %q, want llama then mcp", got)
	}
}

// A slash command resolves only while its extension is loaded (Pi dispatches /llama through the extension runner), so with the
// llama.cpp extension disabled /llama is an ordinary prompt, and with it loaded the registry resolves it.
func TestLlamaSlashCommandResolvesOnlyWhileTheExtensionIsLoaded(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.opts.AgentDir = t.TempDir()
	m.slashRegistry = NewSlashRegistry()
	m.syncExtensionSlashCommands()
	if canonical, ok := m.slashRegistry.Resolve("llama"); ok {
		t.Fatalf("/llama resolves to %q without the llama.cpp extension", canonical)
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{llamaExtension()}, m.opts.AgentDir)
	t.Cleanup(func() { m.newRunner.Invalidate("") })
	m.syncExtensionSlashCommands()
	if _, ok := m.slashRegistry.Resolve("llama"); !ok {
		t.Fatal("/llama does not resolve while the llama.cpp extension is loaded")
	}
}

// Another extension's /llama keeps its own handler (core/extensions/runner.ts resolveRegisteredCommands): with the built-in
// llama.cpp extension also loaded it is llama:1 and runs its handler, not the llama.cpp command.
func TestAnotherExtensionsLlamaCommandRunsItsOwnHandler(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.opts.AgentDir = t.TempDir()
	m.slashRegistry = NewSlashRegistry()
	m.extCtx = &ExtensionContext{}
	m.runCtx = context.Background()
	ran := make(chan string, 1)
	other := extension.Extension{
		Path: "/x/other.ts", CommandOrder: []string{llama.CommandName},
		Commands: map[string]extension.RegisteredCommand{llama.CommandName: {Name: llama.CommandName, Handler: func(_ context.Context, args string) error {
			ran <- args
			return nil
		}}},
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{other, llamaExtension()}, m.opts.AgentDir)
	t.Cleanup(func() { m.newRunner.Invalidate("") })
	m.dispatchSlash(context.Background(), "/llama:1 now")
	select {
	case args := <-ran:
		if args != "now" {
			t.Fatalf("other /llama args = %q", args)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the other extension's /llama handler did not run")
	}
}
