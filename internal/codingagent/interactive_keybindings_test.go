package codingagent

import (
	"context"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func newCustomEditorDispatchMode(t *testing.T) (*InteractiveMode, context.Context) {
	t.Helper()
	renderer := tui.NewWithOutput(io.Discard, 100, 30)
	editor := tui.NewEditor()
	renderer.SetFocus(editor)
	abortCtx, abort := context.WithCancel(context.Background())
	m := &InteractiveMode{
		tuiInst:       renderer,
		editor:        editor,
		keybindings:   DefaultKeybindingsManager(),
		isIdle:        false,
		abortCtx:      abortCtx,
		abortFn:       abort,
		chatContainer: tui.NewContainer(),
	}
	return m, abortCtx
}

// Upstream CustomEditor.handleInput checks each high-priority binding directly:
// extension shortcut, paste-image, interrupt, then exit. It does not let the
// keybinding registry's declaration order decide collisions between them.
func TestCustomEditorHighPriorityBindingOrder(t *testing.T) {
	km := DefaultKeybindingsManager()
	km.SetUserBindings(map[string][]KeyID{
		"app.clipboard.pasteImage": {"ctrl+r"},
		"app.interrupt":            {"ctrl+r"},
		"app.exit":                 {"ctrl+r"},
	})
	if got := classifyKeyWithBindings("\x12", km); got != actionPasteImage {
		t.Fatalf("ctrl+r collision classified as %v, want paste-image before interrupt and exit", got)
	}
}

// This drives InteractiveMode.dispatchKey, the production realization of
// upstream CustomEditor.handleInput, rather than testing only its pure outcome
// table. Each assertion distinguishes the next-lower precedence branch.
func TestCustomEditorDispatchPrecedence(t *testing.T) {
	t.Run("extension shortcut precedes paste-image", func(t *testing.T) {
		m, _ := newCustomEditorDispatchMode(t)
		shortcutCalls := 0
		m.extensionShortcutListener = func(data string) bool {
			if data != "\x16" {
				return false
			}
			shortcutCalls++
			return true
		}

		if err := m.dispatchKey(context.Background(), "\x16"); err != nil {
			t.Fatal(err)
		}
		if shortcutCalls != 1 {
			t.Fatalf("extension shortcut calls = %d, want 1", shortcutCalls)
		}
		if got := m.editor.Text(); got != "" {
			t.Fatalf("consumed paste-image key changed editor text to %q", got)
		}
	})

	t.Run("autocomplete consumes interrupt", func(t *testing.T) {
		m, abortCtx := newCustomEditorDispatchMode(t)
		m.editor.SetAutocomplete(tui.NewSlashOnlyProvider([]tui.SlashCommand{{Name: "settings"}}))
		if err := m.dispatchKey(context.Background(), "/"); err != nil {
			t.Fatal(err)
		}
		if !m.editor.AutocompleteOpen() {
			t.Fatal("slash input did not open autocomplete")
		}
		if err := m.dispatchKey(context.Background(), "\x1b"); err != nil {
			t.Fatal(err)
		}
		if m.editor.AutocompleteOpen() {
			t.Fatal("Escape did not close autocomplete")
		}
		if got := m.editor.Text(); got != "/" {
			t.Fatalf("Escape changed autocomplete input to %q, want slash preserved", got)
		}
		select {
		case <-abortCtx.Done():
			t.Fatal("autocomplete Escape reached the interrupt handler")
		default:
		}
	})

	t.Run("interrupt fires without autocomplete", func(t *testing.T) {
		m, abortCtx := newCustomEditorDispatchMode(t)
		if err := m.dispatchKey(context.Background(), "\x1b"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-abortCtx.Done():
		default:
			t.Fatal("Escape did not abort a working editor without autocomplete")
		}
	})

	t.Run("whitespace is nonempty for exit", func(t *testing.T) {
		m, _ := newCustomEditorDispatchMode(t)
		m.isIdle = true
		m.editor.SetText(" ")
		m.editor.HandleInput("\x01") // Ctrl+A: place the cursor before the space.
		if err := m.dispatchKey(context.Background(), "\x04"); err != nil {
			t.Fatal(err)
		}
		if m.requestExit.Load() {
			t.Fatal("Ctrl+D exited with nonempty whitespace in the editor")
		}
		if got := m.editor.Text(); got != "" {
			t.Fatalf("Ctrl+D left editor text %q, want forward deletion", got)
		}
	})
}
