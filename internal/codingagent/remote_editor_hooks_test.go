package codingagent

import (
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type invalidatingDelegatedEditor struct {
	fakeDelegatedEditor
	invalidations int
}

func (f *invalidatingDelegatedEditor) Invalidate() { f.invalidations++ }

var _ extension.RemoteEditorInvalidator = (*invalidatingDelegatedEditor)(nil)

func handleInputHook(t *testing.T, h extension.RemoteEditorHost, data string, hooks ...string) any {
	t.Helper()
	return baseOp(t, h, "handleInput", map[string]any{"data": data, "hooks": hooks})["hook"]
}

// Pi's CustomEditor.handleInput runs the handler the component set (onEscape, onCtrlD, onPasteImage, an actionHandlers entry) for an app
// action, in place of the one Pi's host copied from the default editor. The host answers a key whose action the component handles with
// the hook, without running the action, and runs the action itself for every other key.
func TestDelegatedEditorHandleInputAnswersTheComponentsHooks(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	h := fake.host
	m.isIdle = true

	// An action the component does not hook runs on the host: app.clear empties the editor.
	m.editor.AsBase(func() { m.editor.SetText("draft") })
	if got := handleInputHook(t, h, "\x03", "app.thinking.cycle"); got != nil || m.editor.Text() != "" {
		t.Fatalf("unhooked app.clear: answer %v, text %q; want the host's clear", got, m.editor.Text())
	}

	// A hooked action does not run: the editor keeps its text where the default handler would clear it.
	m.editor.AsBase(func() { m.editor.SetText("keep") })
	if got := handleInputHook(t, h, "\x03", "app.clear"); got != "app.clear" || m.editor.Text() != "keep" {
		t.Fatalf("hooked app.clear: answer %v, text %q; want the hook and the text kept", got, m.editor.Text())
	}

	// keybindings.ts:143 binds app.clipboard.pasteImage to alt+v with Windows keybindings and to ctrl+v elsewhere.
	pasteKey := "\x16" // Ctrl+V
	if runtime.GOOS == "windows" {
		pasteKey = "\x1bv" // Alt+V
	}
	m.editor.AsBase(func() { m.editor.SetText("") })
	for _, tc := range []struct{ name, key, action string }{
		{"escape", "\x1b", "app.interrupt"},
		{"ctrl+d on an empty editor", "\x04", "app.exit"},
		{"paste image", pasteKey, "app.clipboard.pasteImage"},
	} {
		if got := handleInputHook(t, h, tc.key, tc.action); got != tc.action {
			t.Errorf("%s hooked: handleInput answered %v, want the hook %q", tc.name, got, tc.action)
		}
	}

	// Ctrl+D hands the key to the component's onCtrlD only on an empty editor; otherwise the editor deletes forward.
	m.editor.AsBase(func() { m.editor.SetText("abc") })
	if got := handleInputHook(t, h, "\x04", "app.exit"); got != nil {
		t.Fatalf("ctrl+d on a non-empty editor answered the hook %v", got)
	}
}

// A component that sets its own onExtensionShortcut replaces the host's, which Pi copies only to a component without one.
func TestDelegatedEditorHandleInputSkipsTheHostShortcutsForAComponentWithItsOwn(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	var consulted []string
	m.extensionShortcutListener = func(data string) bool { consulted = append(consulted, data); return false }
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)

	baseOp(t, fake.host, "handleInput", map[string]any{"data": "a"})
	baseOp(t, fake.host, "handleInput", map[string]any{"data": "b", "hooks": []string{"extensionShortcut"}})
	if len(consulted) != 1 || consulted[0] != "a" {
		t.Fatalf("the host's shortcuts were consulted for %q; want only the key of the component without its own", consulted)
	}
}

// The inherited members of Pi's Editor that a custom component reads and sets reach the host's editor.
func TestDelegatedEditorBaseExposesTheEditorsSettings(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	fake := &fakeDelegatedEditor{}
	m.setRemoteEditor(fake)
	h := fake.host

	m.editor.SetPaddingX(3)
	m.editor.SetAutocompleteMaxVisible(9)
	if got := baseOp(t, h, "getPaddingX", nil)["n"]; got != float64(3) {
		t.Errorf("getPaddingX = %v", got)
	}
	if got := baseOp(t, h, "getAutocompleteMaxVisible", nil)["n"]; got != float64(9) {
		t.Errorf("getAutocompleteMaxVisible = %v", got)
	}

	m.editor.BorderColor = func(text string) string { return "<" + text + ">" }
	if got := baseOp(t, h, "borderColor", map[string]string{"text": "──"})["text"]; got != "<──>" {
		t.Errorf("borderColor = %v", got)
	}

	if baseOp(t, h, "getDisableSubmit", nil)["value"] != false {
		t.Error("disableSubmit starts true")
	}
	baseOp(t, h, "setDisableSubmit", map[string]bool{"value": true})
	if !m.editor.DisableSubmit || baseOp(t, h, "getDisableSubmit", nil)["value"] != true {
		t.Errorf("setDisableSubmit left the editor's disableSubmit %v", m.editor.DisableSubmit)
	}

	baseOp(t, h, "setFocused", map[string]bool{"value": false})
	if m.editor.Focused || baseOp(t, h, "getFocused", nil)["value"] != false {
		t.Errorf("setFocused(false) left focused %v", m.editor.Focused)
	}

	var changed []string
	m.editor.OnChange = func(text string) { changed = append(changed, text) }
	baseOp(t, h, "onChange", map[string]string{"text": "typed"})
	if len(changed) != 1 || changed[0] != "typed" {
		t.Errorf("onChange reached the host's callback as %q", changed)
	}
}

// Pi's TUI invalidates every component when the theme changes; the host passes it on to a component that listens.
func TestRemoteEditorPassesAThemeChangeToTheComponent(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.editor.Focused = true
	fake := &invalidatingDelegatedEditor{}
	m.setRemoteEditor(fake)
	m.remoteEditor.Invalidate()
	runPostedTasks(t, m)
	if fake.invalidations != 1 {
		t.Fatalf("the component was invalidated %d times, want 1", fake.invalidations)
	}
}
