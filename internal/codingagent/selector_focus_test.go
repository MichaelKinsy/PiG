package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Upstream showSelector focuses the selector component (interactive-mode.ts setFocus), and OAuthSelectorComponent
// and TreeSelectorComponent propagate that focus to their inputs; the editor regains focus when the selector closes.
func TestEditorSlotOAuthSelectorTakesAndReturnsFocus(t *testing.T) {
	input := make(chan []byte)
	m := &InteractiveMode{editor: tui.NewEditor(), editorContainer: tui.NewContainer(), layout: tui.NewContainer(), modalInputCh: input}
	m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
	m.tuiInst.SetFocus(m.editor)
	sel := tui.NewOAuthSelectorComponent("login", []tui.OAuthProvider{{ID: "a", Name: "A"}}, nil, nil)

	done := make(chan bool)
	go func() { _, ok := m.runEditorSlotOAuthSelector(sel); done <- ok }()
	input <- []byte("x")
	input <- []byte("\x1b[1;1:3A") // a key release is dropped: the previous chunk is fully handled and rendered when this one is received
	if m.tuiInst.GetFocusedComponent() != sel {
		t.Errorf("focus = %T, want the OAuth selector", m.tuiInst.GetFocusedComponent())
	}
	if !strings.Contains(strings.Join(sel.Render(80), "\n"), widthx.CursorMarker) {
		t.Error("search input did not receive the selector's focus")
	}
	input <- []byte("\x1b")
	if <-done {
		t.Error("escape selected a provider")
	}
	if m.tuiInst.GetFocusedComponent() != m.editor {
		t.Errorf("focus after close = %T, want the editor", m.tuiInst.GetFocusedComponent())
	}
}

func TestEditorSlotTreeSelectorTakesAndReturnsFocus(t *testing.T) {
	input := make(chan []byte)
	m := &InteractiveMode{editor: tui.NewEditor(), editorContainer: tui.NewContainer(), layout: tui.NewContainer(), modalInputCh: input}
	m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
	m.tuiInst.SetFocus(m.editor)
	ts := newTestTree()

	done := make(chan bool)
	go func() { _, ok := m.runEditorSlotTreeSelector(ts); done <- ok }()
	input <- []byte("x")
	input <- []byte("\x1b[1;1:3A") // a key release is dropped: the previous chunk is fully handled and rendered when this one is received
	if m.tuiInst.GetFocusedComponent() != ts {
		t.Errorf("focus = %T, want the tree selector", m.tuiInst.GetFocusedComponent())
	}
	input <- []byte("\x1b") // clears the search query
	input <- []byte("\x1b")
	<-done
	if m.tuiInst.GetFocusedComponent() != m.editor {
		t.Errorf("focus after close = %T, want the editor", m.tuiInst.GetFocusedComponent())
	}
}

// SelectAuthProvider receives the choice through the picker's onSelect(providerID, authType), so two entries that
// share an id and differ in auth type stay distinct (oauth-selector.ts onSelectCallback).
func TestSelectAuthProviderReturnsTheEntryOnSelectNamed(t *testing.T) {
	providers := []tui.OAuthProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
	}
	for _, tc := range []struct {
		name   string
		keys   []string
		want   tui.OAuthProvider
		wantOK bool
	}{
		{"down then enter picks the second entry", []string{"\x1b[B", "\r"}, providers[1], true},
		{"cancel returns nothing", []string{"\x1b"}, tui.OAuthProvider{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := make(chan []byte, len(tc.keys))
			for _, key := range tc.keys {
				input <- []byte(key)
			}
			m := &InteractiveMode{runCtx: t.Context(), editor: tui.NewEditor(), editorContainer: tui.NewContainer(), layout: tui.NewContainer(), modalInputCh: input}
			m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
			m.tuiInst.SetFocus(m.editor)
			chosen, ok := m.buildSlashContext(t.Context()).SelectAuthProvider("login", providers, "")
			if ok != tc.wantOK || chosen != tc.want {
				t.Fatalf("chosen %+v ok=%v, want %+v ok=%v", chosen, ok, tc.want, tc.wantOK)
			}
		})
	}
}
