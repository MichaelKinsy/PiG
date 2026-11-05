package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/custom-editor.ts

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// packages/coding-agent/test/custom-editor-keybindings.test.ts "CustomEditor prompt history keybindings" "gives an explicit history binding
// precedence over model cycling": custom-editor.ts hands an explicit tui.editor.historyPrevious/historyNext key to the editor before any app action.
func TestCustomEditorExplicitHistoryBindingBeatsModelCycling(t *testing.T) {
	probe := newCustomEditorProbe(t, `{"tui.editor.historyPrevious":["ctrl+p"],"tui.editor.historyNext":["ctrl+n"]}`)
	modelCycles := 0
	probe.OnAction("app.model.cycleForward", func() { modelCycles++ })
	probe.AddToHistory("previous prompt")
	probe.SetText("draft")

	probe.HandleInput("\x10") // Ctrl+P
	if got := probe.Text(); got != "previous prompt" || modelCycles != 0 {
		t.Fatalf("after Ctrl+P text = %q, model cycles = %d, want the previous prompt and no cycle", got, modelCycles)
	}
	probe.HandleInput("\x0e") // Ctrl+N
	if got := probe.Text(); got != "draft" {
		t.Fatalf("after Ctrl+N text = %q, want draft", got)
	}
}

// packages/coding-agent/test/custom-editor-keybindings.test.ts "CustomEditor selection page keybindings" "pages an open autocomplete menu before
// pasting an image with the same key (#7629)": Ctrl+V is bound to both tui.select.pageDown and app.clipboard.pasteImage.
func TestPR7680AutocompletePageKeysWinOverTheImagePasteShortcut(t *testing.T) {
	probe := newCustomEditorProbe(t, `{"tui.select.pageDown":["ctrl+v"],"app.clipboard.pasteImage":["ctrl+v"]}`)
	commands := make([]tui.SlashCommand, 12)
	for i := range commands {
		commands[i] = tui.SlashCommand{Name: fmt.Sprintf("command-%d", i)}
	}
	probe.SetAutocomplete(tui.NewSlashOnlyProvider(commands))
	pasted := 0
	probe.OnPasteImage = func() { pasted++ }
	var submitted string
	probe.OnSubmit = func(text string) { submitted = text }

	probe.HandleInput("/")
	if !probe.AutocompleteOpen() {
		t.Fatal("autocomplete did not open")
	}
	probe.HandleInput("\x16") // Ctrl+V
	probe.HandleInput("\r")
	if pasted != 0 {
		t.Fatalf("ctrl+v ran the image paste %d times", pasted)
	}
	if submitted != "/command-5" {
		t.Fatalf("submitted %q, want /command-5", submitted)
	}

	probe.HandleInput("\x16") // Ctrl+V with autocomplete closed pastes again
	if pasted != 1 {
		t.Fatalf("ctrl+v with the autocomplete closed pasted %d times, want 1", pasted)
	}
}
