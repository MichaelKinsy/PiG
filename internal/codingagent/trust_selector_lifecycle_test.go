package codingagent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestTrustSelectorBoundaryAndCancellation(t *testing.T) {
	// trust-selector.ts:119-139 clamps navigation and honors remapped confirm/cancel plus literal j/k/newline.
	previous := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	tui.SetKeybindings(tui.NewTUIKeybindingsManager(map[string][]string{tui.KBSelectConfirm: {"ctrl+y"}, tui.KBSelectCancel: {"ctrl+q"}}))
	for _, cwd := range []string{"/", "/parent/project"} {
		t.Run(cwd, func(t *testing.T) {
			var selected *TrustSelection
			cancelled := false
			selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: cwd, OnSelect: func(v TrustSelection) { selected = &v }, OnCancel: func() { cancelled = true }})
			output := stripANSITest(strings.Join(selector.Render(120), "\n"))
			for _, hint := range []string{"ctrl+y save", "ctrl+q cancel"} {
				if !strings.Contains(output, hint) {
					t.Fatalf("missing hint %q in %q", hint, output)
				}
			}
			for range 5 {
				selector.HandleInput("k")
			}
			if selector.selectedIndex != 0 {
				t.Fatal("up wrapped at first choice")
			}
			for range 5 {
				selector.HandleInput("j")
			}
			if selector.selectedIndex != len(selector.trustOptions)-1 {
				t.Fatal("down wrapped at last choice")
			}
			selector.HandleInput("\x19")
			if selected == nil || selected.Trusted {
				t.Fatalf("last choice=%+v", selected)
			}
			selected = nil
			selector.HandleInput("\x11")
			if !cancelled || selected != nil {
				t.Fatal("cancel selected a decision")
			}
		})
	}
}

func TestTrustSelectorModalCancellationRestoresEditor(t *testing.T) {
	for _, input := range []string{"\x1b", "\x03", "", "cancel context"} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			ch := make(chan []byte, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if input == "cancel context" {
				cancel()
			} else if input != "" {
				ch <- []byte(input)
			}
			close(ch)
			m := &InteractiveMode{runCtx: ctx, editor: tui.NewEditor(), editorContainer: tui.NewContainer(), modalInputCh: ch}
			m.editor.SetText("retained draft")
			m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
			m.tuiInst.SetFocus(m.editor)
			selected, ok := m.runTrustSelector(TrustSelectorOptions{Cwd: t.TempDir()})
			if ok || selected.Updates != nil {
				t.Fatal("cancel returned a selection")
			}
			if !strings.Contains(stripANSITest(strings.Join(m.editorContainer.Render(120), "\n")), "retained draft") || m.tuiInst.GetFocusedComponent() != m.editor {
				t.Fatal("modal exit lost the editor draft or focus")
			}
		})
	}
}

func TestTrustSelectorSettlesSelectionBeforeRestoringFocus(t *testing.T) {
	// interactive-mode.ts:5167-5174 saves synchronously before done restores the editor.
	input := make(chan []byte, 1)
	input <- []byte("\n")
	m := &InteractiveMode{editor: tui.NewEditor(), editorContainer: tui.NewContainer(), modalInputCh: input}
	m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
	m.tuiInst.SetFocus(m.editor)
	called := false
	_, ok := m.runTrustSelector(TrustSelectorOptions{Cwd: t.TempDir(), OnSelect: func(TrustSelection) {
		called = true
		if _, focused := m.tuiInst.GetFocusedComponent().(*TrustSelectorComponent); !focused {
			t.Error("editor focus restored before selection settled")
		}
	}})
	if !ok || !called || m.tuiInst.GetFocusedComponent() != m.editor {
		t.Fatal("selection or focus restoration did not complete")
	}
}

func BenchmarkTrustSelector(b *testing.B) {
	selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: "/parent/project"})
	b.ReportAllocs()
	for b.Loop() {
		selector.HandleInput("j")
		selector.Render(120)
		selector.HandleInput("k")
		selector.Render(120)
	}
}
