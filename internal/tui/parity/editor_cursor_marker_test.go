package parity

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type singleSuggestionProvider struct{}

func (singleSuggestionProvider) GetSuggestions(_ context.Context, lines []string, cursorLine, cursorCol int, _ tui.AutocompleteSuggestionOptions) *tui.AutocompleteSuggestions {
	if len(lines) == 0 || !strings.HasPrefix(lines[cursorLine], "/") {
		return nil
	}
	return &tui.AutocompleteSuggestions{
		Items:  []tui.AutocompleteItem{{Value: "help", Label: "help", Description: "show help"}},
		Prefix: lines[cursorLine][:cursorCol],
	}
}

func (singleSuggestionProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item tui.AutocompleteItem, prefix string) ([]string, int, int) {
	line := lines[cursorLine]
	lines[cursorLine] = line[:cursorCol-len(prefix)] + item.Value + line[cursorCol:]
	return lines, cursorLine, cursorCol - len(prefix) + len(item.Value)
}

func TestParityEditorRender_EmitsCursorMarkerWhenFocused(t *testing.T) {
	ed := tui.NewEditor()
	ed.Focused = true
	buf := strings.Join(ed.Render(20), "\n")
	if !strings.Contains(buf, widthx.CursorMarker) {
		t.Fatalf("focused editor render should emit CURSOR_MARKER, got %q", buf)
	}
}

// packages/tui/src/components/editor.ts:569 `const emitCursorMarker = this.focused`: an open autocomplete list does not
// remove the marker, so the hardware cursor stays in the editor line while the list shows.
func TestParityEditorRender_EmitsCursorMarkerWhenAutocompleteOpen(t *testing.T) {
	ed := tui.NewEditor()
	ed.Focused = true
	ed.SetAutocomplete(singleSuggestionProvider{})
	ed.HandleInput("/")
	if !ed.AutocompleteOpen() {
		t.Fatal("the autocomplete list did not open")
	}
	buf := strings.Join(ed.Render(40), "\n")
	if strings.Count(buf, widthx.CursorMarker) != 1 {
		t.Fatalf("editor with open autocomplete should emit one CURSOR_MARKER as Pi's does, got %q", buf)
	}
}

func TestParityEditorTUI_FocusedMarkerBecomesHardwareCursorMove(t *testing.T) {
	var out bytes.Buffer
	ti := tui.NewWithOutput(&out, 20, 6)
	ed := tui.NewEditor()
	ed.Focused = true
	ti.Add(ed)
	ti.Render()
	raw := out.String()
	if strings.Contains(raw, widthx.CursorMarker) {
		t.Fatalf("TUI output should strip CURSOR_MARKER, got %q", raw)
	}
	if !strings.Contains(raw, "\x1b[1G") {
		t.Fatalf("focused editor should drive hardware cursor to column 1, got %q", raw)
	}
}
