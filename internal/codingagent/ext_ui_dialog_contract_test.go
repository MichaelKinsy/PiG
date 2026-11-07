package codingagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// renderSpyTUI counts the renders the interactive mode requests.
type renderSpyTUI struct {
	tui.TUI
	requests atomic.Int32
}

func (s *renderSpyTUI) RequestRender(force ...bool) {
	s.requests.Add(1)
	s.TUI.RequestRender(force...)
}

// activeSelectorSpy stands for the model selector a pending model change still shows: it records how often the dialog stack closes it.
func activeSelectorSpy(m *InteractiveMode) *int {
	closed := new(int)
	m.activeSelectorToken = new(int)
	m.activeSelectorDispose = func() { *closed++ }
	return closed
}

// showExtensionSelector and showExtensionInput (interactive-mode.ts) resolve undefined at once for a signal that has already aborted and show nothing: no dialog joins
// the stack, no selector is closed, the editor keeps the slot and focus, and the program reports no blocked status.
func TestExtensionDialogWithAnAbortedSignalShowsNothing(t *testing.T) {
	calls := map[string]func(ui *ExtUIContext, ctx context.Context) (string, error){
		"select": func(ui *ExtUIContext, ctx context.Context) (string, error) {
			return ui.Select(ctx, "Pick", []string{"a", "b"}, extension.ExtensionUIDialogOptions{})
		},
		"confirm": func(ui *ExtUIContext, ctx context.Context) (string, error) {
			confirmed, err := ui.Confirm(ctx, "Sure?", "message", extension.ExtensionUIDialogOptions{})
			if confirmed {
				return "yes", err
			}
			return "no", err
		},
		"input": func(ui *ExtUIContext, ctx context.Context) (string, error) {
			return ui.Input(ctx, "Name", "", extension.ExtensionUIDialogOptions{})
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			m, _ := newExtensionDialogProbe(t)
			m.runCtx = t.Context()
			terminal := recordProgramStatus(m, "")
			m.tuiInst.SetFocus(m.editor)
			closed := activeSelectorSpy(m)
			ui := &ExtUIContext{m: m}
			ctx, abort := context.WithCancel(t.Context())
			abort()

			result := startDialog(t, m, func() (string, error) { return call(ui, ctx) })
			got := requireResult(t, result, "the dialog with an aborted signal")
			// The cleanup the call posts after it sees the abort also runs on the owner loop.
			for len(m.uiTaskCh) > 0 {
				(<-m.uiTaskCh)()
			}
			if !errors.Is(got.err, context.Canceled) || (got.value != "" && got.value != "no") {
				t.Errorf("dialog = %q, %v; want it cancelled with no value", got.value, got.err)
			}
			if *closed != 0 || openExtensionDialogs(m) != 0 {
				t.Errorf("the aborted dialog closed the active selector %d times and left %d dialogs open; want it to show nothing", *closed, openExtensionDialogs(m))
			}
			requireEditorShown(t, m, "after a dialog with an aborted signal")
			if len(terminal.reports) != 0 {
				t.Errorf("the program reported %+v for a dialog that never showed", terminal.reports)
			}
		})
	}
}

// pushExtensionDialog closes the selector a pending model change still shows (interactive-mode.ts disposeActiveSelector) before the dialog takes the slot: once, and the
// selector is no longer active afterwards.
func TestExtensionDialogClosesTheActiveSelector(t *testing.T) {
	for name, call := range map[string]func(ui *ExtUIContext) (string, error){
		"select": func(ui *ExtUIContext) (string, error) {
			return ui.Select(context.Background(), "Pick", []string{"a"}, extension.ExtensionUIDialogOptions{})
		},
		"input": func(ui *ExtUIContext) (string, error) {
			return ui.Input(context.Background(), "Name", "", extension.ExtensionUIDialogOptions{})
		},
		"editor": func(ui *ExtUIContext) (string, error) { return ui.Editor(context.Background(), "Edit", "text") },
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := newExtensionDialogProbe(t)
			m.runCtx = t.Context()
			closed := activeSelectorSpy(m)
			ui := &ExtUIContext{m: m}
			result := startDialog(t, m, func() (string, error) { return call(ui) })
			if *closed != 1 || m.activeSelectorDispose != nil {
				t.Errorf("the selector was closed %d times and is %v, want closed once and no longer active", *closed, m.activeSelectorDispose != nil)
			}
			if err := m.dispatchKey(t.Context(), "\x1b"); err != nil {
				t.Fatal(err)
			}
			requireResult(t, result, "the cancelled dialog")
			if *closed != 1 {
				t.Errorf("closing the dialog closed the selector again: %d", *closed)
			}
		})
	}
}

// removeExtensionDialog shows the dialog below only when the removed dialog was the visible one: a hidden dialog that ends leaves the slot, the focus and the blocked
// status alone, however the focus was moved since the visible dialog was shown.
func TestRemovingAHiddenExtensionDialogLeavesTheSlotAlone(t *testing.T) {
	r := newCustomRig(t)
	terminal := recordProgramStatus(r.m, "")
	ctxFirst, abortFirst := context.WithCancel(t.Context())
	defer abortFirst()
	first := r.callDialog(t, r.confirmCall(ctxFirst, "First", "first dialog"))
	second := r.callDialog(t, r.confirmCall(r.m.runCtx, "Second", "second dialog"))
	requireVisible(t, r.m, "Second")

	other := &customProbe{label: "OTHER"}
	r.m.tuiInst.SetFocus(other)
	reports := len(terminal.reports)
	abortFirst()
	requireResult(t, first, "the aborted hidden confirm")
	r.pump(t, nil, func() bool { return openExtensionDialogs(r.m) == 1 })
	requireFocus(t, r.m, other, "after the hidden dialog ended")
	if len(terminal.reports) != reports {
		t.Errorf("the program reported %+v when a hidden dialog ended, want nothing new", terminal.reports[reports:])
	}
	requirePending(t, second, "the visible confirm")
	if children := r.m.editorContainer.Children(); len(children) != 1 || children[0] != r.m.topExtensionDialog().component {
		t.Errorf("the editor slot holds %v, want the visible dialog", children)
	}
}

// Every change of the editor slot asks the TUI for a render (showTopExtensionDialog ends with ui.requestRender()): a dialog opening, a dialog closing over another, and
// the last dialog closing.
func TestExtensionDialogsRequestARenderWhenTheSlotChanges(t *testing.T) {
	r := newCustomRig(t)
	spy := &renderSpyTUI{TUI: r.m.tuiInst}
	r.m.tuiInst = spy
	first := r.callDialog(t, r.confirmCall(r.m.runCtx, "First", "first dialog"))
	second := r.callDialog(t, r.confirmCall(r.m.runCtx, "Second", "second dialog"))
	for step, answer := range []struct {
		key    string
		result <-chan dialogCall
	}{{"\x1b", second}, {"\r", first}} {
		before := spy.requests.Load()
		if err := r.m.dispatchKey(t.Context(), answer.key); err != nil {
			t.Fatal(err)
		}
		requireResult(t, answer.result, "the answered confirm")
		if spy.requests.Load() == before {
			t.Errorf("closing dialog %d requested no render", step+1)
		}
	}
	requireEditorShown(t, r.m, "after both dialogs closed")

	spy.requests.Store(0)
	third := r.callDialog(t, r.confirmCall(r.m.runCtx, "Third", "third dialog"))
	if spy.requests.Load() == 0 {
		t.Error("opening a dialog requested no render")
	}
	if err := r.m.dispatchKey(t.Context(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	requireResult(t, third, "the third confirm")
}

// ExtensionInputComponent ignores its placeholder (extension-input.ts names it `_placeholder`): showExtensionInput(title, placeholder) shows the title and an empty
// input, whatever the placeholder says.
func TestExtensionInputIgnoresItsPlaceholder(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.runCtx = t.Context()
	ui := &ExtUIContext{m: m}
	result := startDialog(t, m, func() (string, error) {
		return ui.Input(context.Background(), "Name", "PLACEHOLDER-TEXT", extension.ExtensionUIDialogOptions{})
	})
	shown := widthx.StripAnsi(strings.Join(m.topExtensionDialog().component.Render(60), "\n"))
	if !strings.Contains(shown, "Name") || strings.Contains(shown, "PLACEHOLDER-TEXT") {
		t.Errorf("the input shows %q, want its title and no placeholder", shown)
	}
	if err := m.dispatchKey(t.Context(), "\r"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, result, "the submitted input"); got.value != "" || got.err != nil {
		t.Errorf("an empty submission = %q, %v; want the empty string and no error", got.value, got.err)
	}
}

// showExtensionEditor builds ExtensionEditorComponent with the interactive keybindings, the title, the prefill and the settings' external editor command, and
// the editor is not signal-bound: its dialog stays open until it is submitted or cancelled.
func TestExtensionEditorDialogIsBuiltFromTheModeAndSettings(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.runCtx = t.Context()
	agentDir := t.TempDir()
	m.opts.AgentDir = agentDir
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"externalEditor":"fake-external-editor --wait"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.opts.SettingsManager = NewSettingsManager(m.opts.CWD, agentDir)
	m.keybindings = DefaultKeybindingsManager()
	ui := &ExtUIContext{m: m}
	result := startDialog(t, m, func() (string, error) { return ui.Editor(context.Background(), "Edit this", "prefilled\ntext") })
	editor, ok := m.topExtensionDialog().component.(*ExtensionEditorComponent)
	if !ok {
		t.Fatalf("the dialog is %T, want the extension editor", m.topExtensionDialog().component)
	}
	if editor.externalEditorCommand != "fake-external-editor --wait" {
		t.Errorf("external editor command = %q, want the settings' command", editor.externalEditorCommand)
	}
	if editor.keybindings != m.keybindings {
		t.Error("the editor does not use the interactive mode's keybindings")
	}
	shown := widthx.StripAnsi(strings.Join(editor.Render(60), "\n"))
	if !strings.Contains(shown, "Edit this") || !strings.Contains(shown, "prefilled") || !strings.Contains(shown, "text") {
		t.Errorf("the editor shows %q, want its title and prefill", shown)
	}
	requirePending(t, result, "the open editor")
	if err := m.dispatchKey(t.Context(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	if got := requireResult(t, result, "the cancelled editor"); got.value != "" || !errors.Is(got.err, context.Canceled) {
		t.Errorf("a cancelled editor = %q, %v; want undefined", got.value, got.err)
	}
	requireEditorShown(t, m, "after the editor closed")
}
