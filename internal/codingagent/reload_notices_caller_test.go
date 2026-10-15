package codingagent_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// reloadCallerPair builds a production Session and interactive mode whose reload runs the real slash-context Reload.
func reloadCallerPair(t *testing.T, cwd, agentDir string, autoTrustCwd string, onReloadStart func(), configure ...func(*icodingagent.InteractiveModeOptions)) *icodingagent.TestHarness {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	handler := func(args ...any) (any, error) {
		if event, ok := args[0].(extension.SessionStartEvent); ok && event.Reason == "reload" && onReloadStart != nil {
			onReloadStart()
		}
		return nil, nil
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/reload-probe", Handlers: map[string][]extension.HandlerFn{"session_start": {handler}}}}, services.CWD())
	model := &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}}
	manager, err := coding.NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SessionManager: manager, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	opts := icodingagent.InteractiveModeOptions{
		CWD: services.CWD(), AgentDir: services.AgentDir(), Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(),
		ModelRegistry: services.Registry().ModelRegistry, AutoTrustOnReloadCwd: autoTrustCwd,
		ExtensionRunner: runner, NoSkills: true, NoThemes: true, NoPromptTemplates: true,
	}
	for _, apply := range configure {
		apply(&opts)
	}
	return icodingagent.NewTestHarness(t, opts, nil)
}

// interactive-mode.ts:2016-2018 routes an extension's ctx.reload() through handleReloadCommand, whose streaming guard
// (6394-6397) warns and returns: the extension's promise resolves, and nothing reloads.
func TestExtensionReloadWhileStreamingWarnsAndResolves(t *testing.T) {
	var reloads atomic.Int32
	h := reloadCallerPair(t, t.TempDir(), t.TempDir(), "", func() { reloads.Add(1) })
	h.SetTurnActive(true)
	t.Cleanup(func() { h.SetTurnActive(false) })
	if err := h.ReloadFromExtension(t.Context()); err != nil {
		t.Fatalf("ctx.reload() during a response = %v, want nil", err)
	}
	chat := h.Chat()
	if !strings.Contains(chat, "Warning: Wait for the current response to finish before reloading.") {
		t.Fatalf("transcript lacks the streaming warning:\n%s", chat)
	}
	if reloads.Load() != 0 || strings.Contains(chat, "Reloaded") {
		t.Fatalf("a refused reload ran session_start %d times:\n%s", reloads.Load(), chat)
	}
}

// handleReloadCommand (interactive-mode.ts:6462-6472) runs on the production /reload: after the resource listing it saves
// the implicit trust of a project that gained trust-requiring resources (5260-5285), then shows the models.json error,
// then the status naming the saved trust.
func TestSlashReloadSavesImplicitTrustThenShowsModelsJSONError(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := reloadCallerPair(t, cwd, agentDir, cwd, nil)
	if err := os.MkdirAll(filepath.Join(icodingagent.ProjectConfigDir(cwd), "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.Do(func() { h.Enter("/reload") })
	chat := h.Chat()
	modelsError := strings.Index(chat, "Error: models.json error: Failed to parse models.json")
	status := strings.Index(chat, "Reloaded keybindings, extensions, skills, prompts, themes, and context files; saved project trust")
	if modelsError < 0 || status < 0 || modelsError > status {
		t.Fatalf("models.json error (%d) must precede the saved-trust status (%d):\n%s", modelsError, status, chat)
	}
	decision, err := icodingagent.NewProjectTrustStore(agentDir).Get(cwd)
	if err != nil || decision == nil || !*decision {
		t.Fatalf("saved decision = %v, %v", decision, err)
	}

	// The decision is saved once: a second reload reports the plain status.
	h.Do(func() { h.Enter("/reload") })
	if chat := h.Chat(); !strings.Contains(chat, "Reloaded keybindings") || strings.Contains(chat, "saved project trust") {
		t.Fatalf("second reload status:\n%s", chat)
	}
}

// main.ts:719-722 arms the reload trust save only for a start without a trust override and without trust-requiring
// resources; an unarmed mode never saves.
func TestSlashReloadWithoutArmedCwdSavesNoTrust(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	h := reloadCallerPair(t, cwd, agentDir, "", nil)
	if err := os.MkdirAll(filepath.Join(icodingagent.ProjectConfigDir(cwd), "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.Do(func() { h.Enter("/reload") })
	chat := h.Chat()
	if !strings.Contains(chat, "Reloaded keybindings") || strings.Contains(chat, "saved project trust") {
		t.Fatalf("status:\n%s", chat)
	}
	if decision, err := icodingagent.NewProjectTrustStore(agentDir).Get(cwd); err != nil || decision != nil {
		t.Fatalf("unarmed reload saved %v, %v", decision, err)
	}
}

// A trust store that cannot be read warns from maybeSaveImplicitProjectTrustAfterReload before the models.json error
// (interactive-mode.ts:6463-6467), and the status does not name a saved decision.
func TestSlashReloadTrustSaveFailureWarnsBeforeModelsJSONError(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := reloadCallerPair(t, cwd, agentDir, cwd, nil)
	if err := os.MkdirAll(filepath.Join(icodingagent.ProjectConfigDir(cwd), "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "trust.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Do(func() { h.Enter("/reload") })
	chat := h.Chat()
	warning := strings.Index(chat, "Warning: Could not save project trust after reload: ")
	modelsError := strings.Index(chat, "Error: models.json error: ")
	if warning < 0 || modelsError < 0 || warning > modelsError {
		t.Fatalf("trust warning (%d) must precede the models.json error (%d):\n%s", warning, modelsError, chat)
	}
	if !strings.Contains(chat, "Reloaded keybindings") || strings.Contains(chat, "saved project trust") {
		t.Fatalf("status:\n%s", chat)
	}
}

// An extension's ctx.reload() runs handleReloadCommand (interactive-mode.ts:2016-2018), which shows the reload status
// (6468-6472) as /reload does.
func TestExtensionReloadShowsTheReloadStatus(t *testing.T) {
	var reloads atomic.Int32
	h := reloadCallerPair(t, t.TempDir(), t.TempDir(), "", func() { reloads.Add(1) })
	if err := h.ReloadFromExtension(t.Context()); err != nil {
		t.Fatal(err)
	}
	if chat := h.Chat(); reloads.Load() != 1 || !strings.Contains(chat, "Reloaded keybindings, extensions, skills, prompts, themes, and context files") {
		t.Fatalf("ctx.reload() ran session_start %d times; transcript:\n%s", reloads.Load(), chat)
	}
}

// handleReloadCommand catches a failed reload, shows "Reload failed: <message>" (interactive-mode.ts:6474-6479) and
// returns normally, so an extension's awaited ctx.reload() resolves.
func TestExtensionReloadFailureIsShownAndResolves(t *testing.T) {
	h := reloadCallerPair(t, t.TempDir(), t.TempDir(), "", nil, func(opts *icodingagent.InteractiveModeOptions) {
		opts.ReloadResourceProvider = func() icodingagent.ReloadResourceSnapshot {
			return icodingagent.ReloadResourceSnapshot{Err: errors.New("resource resolution failed")}
		}
	})
	if err := h.ReloadFromExtension(t.Context()); err != nil {
		t.Fatalf("ctx.reload() = %v, want the failure shown and nil", err)
	}
	chat := h.Chat()
	if !strings.Contains(chat, "Error: Reload failed: resource resolution failed") || strings.Contains(chat, "Reloaded keybindings") {
		t.Fatalf("transcript:\n%s", chat)
	}
}
