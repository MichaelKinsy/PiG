package coding

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:3547-3567 (_buildRuntime) and 3591-3609: the rebuild replaces the runner, the old one goes stale, and the tool registry is rebuilt from the new runner's tools, activating the tools newly added to defaultTools.
func TestReloadExtensionsReplacesTheRunnerAndRebuildsTools(t *testing.T) {
	session := newRegistryPortSession(t, nil, SessionOptions{}, []extension.ToolDefinition{inactiveRegistryTool()}, nil)
	bindRegistryPort(t, session)
	previous := session.ExtensionRunner()
	writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"+grep"}})
	session.ReloadSettings()

	added := registryTool("added_tool", "Added Tool", "Registered by the reloaded extension", "")
	runner, err := session.ReloadExtensions([]extension.Extension{{Path: "<reload:1>", Tools: map[string]extension.RegisteredTool{"added_tool": {Definition: added}}}})
	if err != nil {
		t.Fatal(err)
	}
	if runner == previous || session.ExtensionRunner() != runner {
		t.Fatal("the Session did not switch to the new runner")
	}
	if previous.StaleMessage() == "" {
		t.Fatal("the replaced runner must go stale")
	}
	active := session.ActiveToolNames()
	if !slices.Contains(active, "grep") || !slices.Contains(active, "added_tool") {
		t.Fatalf("active = %q, want the defaultTools addition and the reloaded extension's tool", active)
	}
	if slices.Contains(allRegistryNames(session), "inactive_tool") {
		t.Fatal("a tool of the replaced runner stayed in the registry")
	}
}

// agent-session.ts:3596 syncQueueModesFromSettings: a settings reload applies the queue drain modes to the agent.
func TestReloadSettingsSyncsQueueModes(t *testing.T) {
	session := newRegistryPortSession(t, nil, SessionOptions{}, nil, nil)
	writeRegistryPortSettings(t, session, map[string]any{"steeringMode": "all", "followUpMode": "all"})
	session.ReloadSettings()
	if session.agent.SteeringMode() != agent.QueueMode("all") || session.agent.FollowUpMode() != agent.QueueMode("all") {
		t.Fatalf("modes = %q, %q, want all, all", session.agent.SteeringMode(), session.agent.FollowUpMode())
	}
}

// Runtime.ExtensionCommandActions binds ctx.reload() in every mode that runs a Session (print-mode.ts:97-99, rpc-mode.ts:341-343); without the mode's reload it fails instead of doing nothing.
func TestRuntimeReloadActionRunsTheModesReload(t *testing.T) {
	rt := &Runtime{}
	session := newRegistryPortSession(t, nil, SessionOptions{}, nil, nil)
	actions := rt.ExtensionCommandActions(session)
	if actions.ReloadContext == nil || actions.Reload == nil {
		t.Fatal("reload is not bound")
	}
	if err := actions.Reload(); err == nil {
		t.Fatal("a Runtime without a mode reload must fail the call")
	}
	var got *Session
	rt.SetReload(func(_ context.Context, s *Session) error { got = s; return nil })
	if err := actions.ReloadContext(t.Context()); err != nil || got != session {
		t.Fatalf("reload ran for %p with %v, want %p", got, err, session)
	}
}

// agent-session.ts:3604-3609 rebuilds the runtime with includeAllExtensionTools, so without an allowlist a reload activates every extension or SDK tool that activates on registration (3507-3510), also one disabled during the session. Probed with Pi 0.99.2 in print mode: an extension tool removed with setActiveTools(["read","bash"]) is active again in session_start(reload) ("read,bash,footool"), while the disabled built-ins stay off.
func TestReloadExtensionsReactivatesExtensionToolsDisabledDuringTheSession(t *testing.T) {
	tool := registryTool("ext_tool", "Ext Tool", "Extension tool", "")
	extensions := func() []extension.Extension {
		return []extension.Extension{{Path: "<reload:1>", Tools: map[string]extension.RegisteredTool{"ext_tool": {Definition: tool}}}}
	}
	t.Run("without an allowlist", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{}, []extension.ToolDefinition{tool}, nil)
		bindRegistryPort(t, session)
		session.SetActiveToolsByName([]string{"read", "bash"})
		if _, err := session.ReloadExtensions(extensions()); err != nil {
			t.Fatal(err)
		}
		if got, want := session.ActiveToolNames(), []string{"read", "bash", "ext_tool"}; !slices.Equal(got, want) {
			t.Fatalf("active = %q, want %q", got, want)
		}
	})
	t.Run("an allowlist keeps naming the tools", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: map[string]struct{}{"read": {}}}, []extension.ToolDefinition{tool}, nil)
		bindRegistryPort(t, session)
		if _, err := session.ReloadExtensions(extensions()); err != nil {
			t.Fatal(err)
		}
		if got := session.ActiveToolNames(); slices.Contains(got, "ext_tool") {
			t.Fatalf("active = %q, want the allowlist to exclude ext_tool", got)
		}
	})
	t.Run("a refresh outside a reload keeps the selection", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{}, []extension.ToolDefinition{tool}, nil)
		bindRegistryPort(t, session)
		session.SetActiveToolsByName([]string{"read", "bash"})
		if err := session.RefreshTools(); err != nil {
			t.Fatal(err)
		}
		if got := session.ActiveToolNames(); slices.Contains(got, "ext_tool") {
			t.Fatalf("active = %q, want ext_tool to stay disabled", got)
		}
	})
}
