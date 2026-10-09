package inproc_test

import (
	"context"
	"encoding/json"
	"github.com/MichaelKinsy/PiG/ai"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi: packages/coding-agent/src/core/keybindings.ts:391 (KeybindingsManager.getResolvedBindings).
// Pi: packages/coding-agent/src/core/extensions/types.ts:2057 (ExtensionShortcut.description).
func TestUpstreamRunnerShortcuts(t *testing.T) {
	defaults := codingagent.DefaultKeybindingsManager().GetResolvedBindings()
	paste := defaults["app.clipboard.pasteImage"][0]
	for _, tc := range []struct {
		name, key, description, action string
		keys                           []string
		warning                        string
		allowed                        bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:169
		{name: "warns when extension shortcut conflicts with built-in", key: "ctrl+c", description: "Conflicts with built-in", warning: "conflicts with built-in"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:192
		{name: "allows a shortcut when the reserved set no longer contains the default key", key: "ctrl+p", description: "Uses freed default", action: "app.model.cycleForward", keys: []string{"ctrl+n"}, allowed: true},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:216
		{name: "warns but allows when extension uses non-reserved built-in shortcut", key: paste, description: "Overrides non-reserved", warning: "built-in shortcut for app.clipboard.pasteImage", allowed: true},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:244
		{name: "blocks shortcuts for reserved actions even when rebound", key: "ctrl+x", description: "Conflicts with rebound reserved", action: "app.interrupt", keys: []string{"ctrl+x"}, warning: "conflicts with built-in"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:268
		{name: "blocks shortcuts when reserved key is also bound to non-reserved actions", key: "ctrl+p", description: "Conflicts with shared reserved default", warning: "conflicts with built-in"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:291
		{name: "blocks shortcuts when reserved action has multiple keys", key: "ctrl+y", description: "Conflicts with multi-key reserved", action: "app.clear", keys: []string{"ctrl+x", "ctrl+y"}, warning: "conflicts with built-in"},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:315
		{name: "warns but allows when non-reserved action has multiple keys", key: "ctrl+y", description: "Overrides multi-key non-reserved", action: "app.clipboard.pasteImage", keys: []string{"ctrl+x", "ctrl+y"}, warning: "built-in shortcut for app.clipboard.pasteImage", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := maps.Clone(defaults)
			if tc.action != "" {
				bindings[tc.action] = tc.keys
			}
			r := inproc.NewRunner([]extension.Extension{extWithShortcut("extension.ts", tc.key, tc.description)}, t.TempDir())
			_, present := r.Shortcuts(bindings)[tc.key]
			if present != tc.allowed {
				t.Fatalf("shortcut present=%v, want %v", present, tc.allowed)
			}
			diagnostics := r.ShortcutDiagnostics()
			if tc.warning == "" {
				if slices.ContainsFunc(diagnostics, func(d extension.ResourceDiagnostic) bool {
					return strings.Contains(d.Message, "conflicts with built-in")
				}) {
					t.Fatalf("unexpected warning: %v", diagnostics)
				}
			} else if !slices.ContainsFunc(diagnostics, func(d extension.ResourceDiagnostic) bool { return strings.Contains(d.Message, tc.warning) }) {
				t.Fatalf("warnings=%v, want %q", diagnostics, tc.warning)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:341
	t.Run("warns when two extensions register same shortcut", func(t *testing.T) {
		r := inproc.NewRunner([]extension.Extension{extWithShortcut("ext1.ts", "ctrl+shift+x", "First extension"), extWithShortcut("ext2.ts", "ctrl+shift+x", "Second extension")}, t.TempDir())
		shortcut, present := r.Shortcuts(defaults)["ctrl+shift+x"]
		if !present || shortcut.Description != "Second extension" {
			t.Fatalf("last shortcut=%+v, present=%v", shortcut, present)
		}
		if !slices.ContainsFunc(r.ShortcutDiagnostics(), func(d extension.ResourceDiagnostic) bool { return strings.Contains(d.Message, "shortcut conflict") }) {
			t.Fatalf("warnings=%v", r.ShortcutDiagnostics())
		}
	})
}

func upstreamRunnerTool(path, name, description string) extension.Extension {
	ext := newFakeExtension(path)
	ext.Tools[name] = extension.RegisteredTool{Definition: extension.ToolDefinition{
		Name: name, Label: name, Description: description, Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return extension.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}, nil
		},
	}}
	return ext
}

func TestUpstreamRunnerCatalog(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:377
	t.Run("collects tools from multiple extensions", func(t *testing.T) {
		r := inproc.NewRunner([]extension.Extension{upstreamRunnerTool("tool-a.ts", "tool_a", "Test tool"), upstreamRunnerTool("tool-b.ts", "tool_b", "Test tool")}, t.TempDir())
		tools := r.Tools()
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Definition.Name
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{"tool_a", "tool_b"}) {
			t.Fatalf("tools=%v", names)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:427
	t.Run("keeps first tool when two extensions register the same name", func(t *testing.T) {
		r := inproc.NewRunner([]extension.Extension{upstreamRunnerTool("a-first.ts", "shared", "first"), upstreamRunnerTool("b-second.ts", "shared", "second")}, t.TempDir())
		tools := r.Tools()
		if len(tools) != 1 || tools[0].Definition.Description != "first" {
			t.Fatalf("tools=%+v", tools)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:465
	t.Run("collects commands from multiple extensions", func(t *testing.T) {
		r := inproc.NewRunner([]extension.Extension{extWithCommand("cmd-a.ts", "cmd-a", "Test command"), extWithCommand("cmd-b.ts", "cmd-b", "Test command")}, t.TempDir())
		commands := r.Commands()
		names := make([]string, len(commands))
		invocations := make([]string, len(commands))
		for i, c := range commands {
			names[i] = c.Name
			invocations[i] = c.InvocationName
		}
		slices.Sort(names)
		slices.Sort(invocations)
		if !slices.Equal(names, []string{"cmd-a", "cmd-b"}) || !slices.Equal(invocations, []string{"cmd-a", "cmd-b"}) {
			t.Fatalf("names=%v invocations=%v", names, invocations)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:486
	t.Run("gets command by invocation name", func(t *testing.T) {
		r := inproc.NewRunner([]extension.Extension{extWithCommand("cmd.ts", "my-cmd", "My command")}, t.TempDir())
		c, present := r.Command("my-cmd")
		if !present || c.Name != "my-cmd" || c.InvocationName != "my-cmd" || c.Description != "My command" {
			t.Fatalf("command=%+v present=%v", c, present)
		}
		if _, present := r.Command("not-exists"); present {
			t.Fatal("missing command found")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:510
	t.Run("suffixes duplicate extension commands in insertion order", func(t *testing.T) {
		descriptions := []string{"First command", "Second command"}
		r := inproc.NewRunner([]extension.Extension{extWithCommand("cmd-a.ts", "shared-cmd", descriptions[0]), extWithCommand("cmd-b.ts", "shared-cmd", descriptions[1])}, t.TempDir())
		commands := r.Commands()
		if len(commands) != len(descriptions) {
			t.Fatalf("commands=%+v", commands)
		}
		for i, invocation := range []string{"shared-cmd:1", "shared-cmd:2"} {
			c := commands[i]
			if c.Name != "shared-cmd" || c.InvocationName != invocation || c.Description != descriptions[i] {
				t.Fatalf("command[%d]=%+v", i, c)
			}
			found, ok := r.Command(invocation)
			if !ok || found.Description != descriptions[i] {
				t.Fatalf("lookup %q=%+v, ok=%v", invocation, found, ok)
			}
		}
		if len(r.CommandDiagnostics()) != 0 {
			t.Fatalf("diagnostics=%v", r.CommandDiagnostics())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1343
	t.Run("returns true when handlers exist for event type", func(t *testing.T) {
		r := inproc.NewRunner([]extension.Extension{extWithHandler("handler.ts", "tool_call")}, t.TempDir())
		if !r.HasHandlers("tool_call") || r.HasHandlers("agent_end") {
			t.Fatal("handler presence mismatch")
		}
	})
}
