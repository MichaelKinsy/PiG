package tui

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// editorTUI is the part of the TUI an Editor uses (editor.ts `tui`): requestRender and terminal.rows.
type editorTUI struct {
	TUI
	terminal editorTerminal
	renders  int
}

type editorTerminal struct {
	Terminal
	rows int
}

func (t editorTerminal) Rows() int { return t.rows }

func (u *editorTUI) Terminal() Terminal    { return u.terminal }
func (u *editorTUI) RequestRender(...bool) { u.renders++ }
func newOwnedEditor(rows int, options EditorOptions) (*Editor, *editorTUI) {
	ui := &editorTUI{terminal: editorTerminal{rows: rows}}
	return NewEditorWithTUI(ui, EditorTheme{BorderColor: func(s string) string { return s }}, options), ui
}

func renderedTextRows(e *Editor) int {
	n := 0
	for _, line := range e.Render(40) {
		if strings.Contains(line, "line ") {
			n++
		}
	}
	return n
}

// editor.ts:535-537 derives the visible window from the owned tui's terminal rows: max(5, floor(rows * 0.3)). A host-driven editor keeps the window the host sets.
func TestEditorOwnedTUIDerivesTheVisibleWindowFromTerminalRows(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat("line x\n", 40), "\n")
	for _, tc := range []struct{ rows, want int }{{10, 5}, {40, 12}, {100, 30}} {
		e, _ := newOwnedEditor(tc.rows, EditorOptions{})
		e.SetText(text)
		if got := renderedTextRows(e); got != tc.want {
			t.Errorf("rows=%d: %d text rows rendered, want %d", tc.rows, got, tc.want)
		}
	}
	host := NewEditor()
	host.SetText(text)
	host.SetMaxVisibleLines(7)
	if got := renderedTextRows(host); got != 7 {
		t.Errorf("host-driven editor rendered %d rows, want the 7 the host set", got)
	}
}

// editor.ts:1961-1963 pages by max(5, floor(rows * 0.3)) visual lines of the owned tui.
func TestEditorOwnedTUIPagesByTerminalRows(t *testing.T) {
	e, _ := newOwnedEditor(100, EditorOptions{})
	e.SetText(strings.TrimSuffix(strings.Repeat("line x\n", 80), "\n"))
	e.Render(40)
	e.pageScroll(-1)
	if got, want := e.cursor[0], 79-30; got != want {
		t.Fatalf("page up from the last line moved to row %d, want %d", got, want)
	}
}

// editor.ts:395-414 setPaddingX and setAutocompleteMaxVisible request a render on the owned tui only when the value changes; the constructor clamps its options (:377-380).
func TestEditorOwnedTUIRequestsRenderOnlyWhenPaddingOrLimitChanges(t *testing.T) {
	zero, huge := 0, 99
	e, ui := newOwnedEditor(40, EditorOptions{PaddingX: new(-3), AutocompleteMaxVisible: &huge})
	if e.paddingX != 0 || e.AutocompleteMaxVisible() != 20 {
		t.Fatalf("constructor options: paddingX=%d autocompleteMax=%d, want 0 and 20", e.paddingX, e.AutocompleteMaxVisible())
	}
	if ui.renders != 0 {
		t.Fatalf("the constructor requested %d renders", ui.renders)
	}
	e.SetPaddingX(zero)
	e.SetAutocompleteMaxVisible(20)
	if ui.renders != 0 {
		t.Fatalf("an unchanged padding and limit requested %d renders", ui.renders)
	}
	e.SetPaddingX(2)
	e.SetAutocompleteMaxVisible(8)
	if ui.renders != 2 {
		t.Fatalf("a changed padding and limit requested %d renders, want 2", ui.renders)
	}
	if low := NewEditorWithTUI(ui, EditorTheme{}, EditorOptions{AutocompleteMaxVisible: new(1)}); low.AutocompleteMaxVisible() != 3 {
		t.Fatalf("a limit of 1 clamps to %d, want 3", low.AutocompleteMaxVisible())
	}
}

// editor.ts:2412 requests a render on the owned tui once an autocomplete result has been applied; a stale result applies nothing and requests none (editor.ts:2380).
func TestEditorOwnedTUIRequestsRenderAfterAnAutocompleteResultApplies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider := &AsyncAutocompleteProvider{TriggerCharacters: []string{"$"}, GetSuggestions: func(context.Context, []string, int, int, bool) (*AutocompleteSuggestions, error) {
			return &AutocompleteSuggestions{Prefix: "$", Items: []AutocompleteItem{{Value: "$home", Label: "$home"}}}, nil
		}}
		p := newAsyncEditorProbe(t, provider)
		ui := &editorTUI{terminal: editorTerminal{rows: 40}}
		p.editor.ui = ui
		p.editor.HandleInput("$x")
		time.Sleep(attachmentAutocompleteDebounce)
		if ui.renders != 0 {
			t.Fatalf("a render was requested before the result was applied: %d", ui.renders)
		}
		p.drain()
		if len(p.editor.autocompleteItems) != 1 || ui.renders == 0 {
			t.Fatalf("items=%d renders=%d, want the result applied and a render requested", len(p.editor.autocompleteItems), ui.renders)
		}
	})
}
