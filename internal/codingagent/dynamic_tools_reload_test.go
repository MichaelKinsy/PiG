package codingagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi rebuilds the initial registry before reload's session_start. A name from the retired factory is newly registered again, even if the old tool was disabled.
func TestReloadDynamicToolsClearsRetiredRegistry(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var session *coding.Session
	factory := func() extension.Extension {
		ext := extension.Extension{Name: "dynamic", Tools: map[string]extension.RegisteredTool{}}
		ext.InitializeToolRegistry()
		ext.Handlers = map[string][]extension.HandlerFn{"session_start": {func(...any) (any, error) {
			ext.SetRegisteredTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "late", Description: "dynamic", Parameters: []byte(`{}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
				return nil, nil
			}}})
			return nil, session.RefreshTools()
		}}}
		return ext
	}
	ext := factory()
	runner := inproc.NewRunner([]extension.Extension{ext}, services.CWD())
	session, err = coding.NewSession(services, coding.SessionOptions{Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionHandle: session, SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner, BuiltinExtensions: []extension.Extension{ext}, ReloadBuiltinExtensions: func() []extension.Extension { return []extension.Extension{factory()} }, NoSkills: true, NoThemes: true, NoPromptTemplates: true, ActiveBuiltinTools: map[string]struct{}{}}, nil)
	if err := session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	session.SetActiveToolsByName(nil)
	if err := h.ReloadFromExtension(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(session.ActiveToolNames(), "late") {
		t.Fatalf("fresh session_start tool remained disabled: %v", session.ActiveToolNames())
	}
}

// agent-session.ts:3591-3609 (#10245): the interactive /reload path reloads settings through the Session, so a tool the setting newly adds is active after the runtime rebuild, a removed one stays active, and a tool disabled during the session stays off.
func TestReloadActivatesToolsNewlyAddedToDefaultTools(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionHandle: session, SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), NoSkills: true, NoThemes: true, NoPromptTemplates: true}, nil)
	if err := session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	session.SetActiveToolsByName([]string{"read", "edit", "write"})
	settingsPath := filepath.Join(services.AgentDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"defaultTools":["+grep","-read"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.ReloadFromExtension(t.Context()); err != nil {
		t.Fatal(err)
	}
	active := session.ActiveToolNames()
	slices.Sort(active)
	if want := []string{"edit", "grep", "read", "write"}; !slices.Equal(active, want) {
		t.Fatalf("active after reload = %q, want %q (grep added, read kept, bash still off)", active, want)
	}
}
