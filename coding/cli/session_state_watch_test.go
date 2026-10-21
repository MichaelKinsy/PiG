package cli

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's ctx.model, pi.getSettings(), pi.getSessionName() and ctx.ui.theme read the live Session, settings and global theme (runner.ts:906, loader.ts:411-414, interactive-mode.ts:2665), so the modes that run extensions report each change of the replicated host state to the extension host: a runtime that reads between two requests then holds the new value.
func TestWatchHostStateReportsSessionAndThemeChangesUntilStopped(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	session := newModelRegistryTestSession(t, services)
	bridge := subprocess.NewUIBridge(func() {})
	var reports atomic.Int64
	bridge.OnHostStateChanged = func() { reports.Add(1) }
	previousTheme := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeInstance(previousTheme) })

	stop := coding.WatchHostState(bridge, session)
	for _, step := range []struct {
		name   string
		change func() error
	}{
		{"the session name", func() error { return session.SetSessionName("renamed") }},
		{"a setting", func() error { return services.SettingsManager().SetEditorPaddingX(2) }},
		{"the thinking level", func() error { return session.SetThinkingLevel(ai.ThinkingHigh) }},
		{"the active theme", func() error { tui.SetTheme("light"); return nil }},
	} {
		before := reports.Load()
		if err := step.change(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if reports.Load() == before {
			t.Errorf("a change of %s was not reported to the extension host", step.name)
		}
	}
	stop()
	before := reports.Load()
	if err := session.SetSessionName("renamed again"); err != nil {
		t.Fatal(err)
	}
	if reports.Load() != before {
		t.Fatal("a stopped watch still reported")
	}
	tui.SetTheme("dark")
	if reports.Load() != before {
		t.Fatal("a stopped watch still reported a theme change")
	}
}

// Without an extension host or a Session there is nothing to report to or from, and the stop function is still safe to call.
func TestWatchHostStateWithoutABridgeOrSession(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	session := newModelRegistryTestSession(t, services)
	coding.WatchHostState(nil, session)()
	coding.WatchHostState(subprocess.NewUIBridge(func() {}), nil)()
}

// settingsEditTool edits the settings from inside a run, as a user's /settings edit changes them with no extension call.
type settingsEditTool struct{ edit func() }

func (settingsEditTool) Name() string                           { return "settings_edit_tool" }
func (settingsEditTool) Label() string                          { return "settings edit tool" }
func (settingsEditTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (settingsEditTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "settings_edit_tool", Parameters: map[string]any{"type": "object"}}
}
func (t settingsEditTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	t.edit()
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "edited"}}}, nil
}

// Print and JSON mode bind the Session to the extension host as rebindSession does, and report the Session's replicated state changes to it from then on.
func TestPrintModeReportsSessionStateChangesToTheExtensionHost(t *testing.T) {
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(
				ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("settings_edit_tool", map[string]any{}, &ai.FauxToolCallOptions{ID: "edit-1"})}, StopReason: "toolUse"},
				fauxTextResponse("done"),
			))
			host := printModeTestHost(t, provider)
			host.Bridge = subprocess.NewUIBridge(func() {})
			var reports atomic.Int64
			host.Bridge.OnHostStateChanged = func() { reports.Add(1) }
			var reportedByTheEdit atomic.Bool
			host.Session.ExtraTools = []agent.AgentTool{settingsEditTool{edit: func() {
				before := reports.Load()
				if err := host.Services.SettingsManager().SetEditorPaddingX(2); err != nil {
					t.Error(err)
				}
				reportedByTheEdit.Store(reports.Load() > before)
			}}}
			result := runPrintModeForTest(t, host, printModeOptions{Mode: mode, InitialMessage: "edit the settings"})
			if result.err != nil || result.stderr != "" {
				t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
			}
			if !reportedByTheEdit.Load() {
				t.Fatal("the settings edit was not reported to the extension host")
			}
		})
	}
}
