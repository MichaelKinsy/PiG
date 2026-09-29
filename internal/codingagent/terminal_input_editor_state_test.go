package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func BenchmarkTerminalInputEditorSnapshotRead(b *testing.B) {
	for _, size := range []int{0, 1000, 64 << 10, 1 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			mode := &InteractiveMode{editor: tui.NewEditor()}
			mode.editor.HandleInput("\x1b[200~" + strings.Repeat("x", size) + "\x1b[201~")
			mode.bindEditorSnapshot()
			ui := &ExtUIContext{m: mode}
			b.ReportAllocs()
			for b.Loop() {
				if len(ui.GetEditorText()) != size {
					b.Fatal("expanded snapshot lost paste data")
				}
			}
		})
	}
}

// Pi interactive-mode.ts:2560 exposes getExpandedText rather than the compact editor marker. The owner must publish that same value before a subprocess terminal callback runs.
func TestTerminalInputSnapshotKeepsExpandedPasteContents(t *testing.T) {
	q := newInputQueueMode(t)
	q.bindEditorSnapshot()
	ext := attachRemoteInputExtension(t, q.InteractiveMode, "editor-state")
	q.start(t)
	for _, row := range []struct {
		name, text        string
		compact, expanded bool
	}{
		{"empty", "", false, false},
		{"ordinary", "draft", false, true},
		{"character-boundary", strings.Repeat("x", 1000), false, false},
		{"compact-characters", strings.Repeat("x", 1001), true, true},
		{"compact-lines", strings.Repeat("a\n", 10) + "z", true, false},
		{"compact-utf16", strings.Repeat("😀", 501), true, true},
		{"reset", "", false, false},
		{"replacement", "replacement", false, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			visible := runOnQueueLoop(t, q.InteractiveMode, func() string {
				q.editor.Clear()
				if row.text != "" {
					q.editor.HandleInput("\x1b[200~" + row.text + "\x1b[201~")
				}
				q.toolsExpanded = row.expanded
				return q.editor.Text()
			})
			if compact := strings.Contains(visible, "[paste #"); compact != row.compact {
				t.Fatalf("fixture compact=%t; want %t", compact, row.compact)
			}
			q.typeKeys(t, "~")
			request := ext.next(t, "~")
			ext.answer(t, request, map[string]any{"consume": true})
			if request.state.EditorText != row.text || request.state.ToolsExpanded != row.expanded {
				t.Errorf("wire editor=%q expanded=%t; want editor=%q expanded=%t", request.state.EditorText, request.state.ToolsExpanded, row.text, row.expanded)
			}
			if native := runOnQueueLoop(t, q.InteractiveMode, func() string { return (&ExtUIContext{m: q.InteractiveMode}).GetEditorText() }); native != row.text {
				t.Fatalf("native editor=%q; want %q", native, row.text)
			}
		})
	}
}
