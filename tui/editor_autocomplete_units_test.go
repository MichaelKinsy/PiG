package tui

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type utf16CompletionProbe struct {
	t           *testing.T
	wantUnits   []uint16
	wantByteCol int
	prefix      string
	calls       int
}

func (p *utf16CompletionProbe) GetSuggestions(ctx context.Context, lines []string, row, col int, options AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	p.t.Helper()
	p.calls++
	if row != 0 || col != p.wantByteCol || !slices.Equal(jsstring.ToUTF16(lines[0]), p.wantUnits) {
		p.t.Fatalf("provider view row=%d col=%d units=%04x want byteCol=%d units=%04x", row, col, jsstring.ToUTF16(lines[0]), p.wantByteCol, p.wantUnits)
	}
	return &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "Q", Label: "Q"}}, Prefix: p.prefix}
}
func (p *utf16CompletionProbe) ApplyCompletion(lines []string, row, col int, item AutocompleteItem, prefix string) ([]string, int, int) {
	p.t.Helper()
	if col != p.wantByteCol || prefix != p.prefix {
		p.t.Fatalf("apply byte column=%d prefix=%x", col, prefix)
	}
	out := slices.Clone(lines)
	out[row] = lines[row][:col-len(prefix)] + item.Value + lines[row][col:]
	return out, row, col - len(prefix) + len(item.Value)
}

func TestEditorUTF16CoordinatesPreserveProviderByteContract(t *testing.T) {
	for _, tc := range []struct {
		name, text, prefix string
		cursor, byteCol    int
		want               []uint16
	}{
		{"BMP", "äX", "ä", 1, 2, []uint16{'Q', 'X'}},
		{"astral boundary", "😀X", "😀", 2, 4, []uint16{'Q', 'X'}},
		{"inside pair", "😀X", jsstring.FromUTF16([]uint16{0xd83d}), 1, 3, []uint16{'Q', 0xde00, 'X'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			e.SetText(tc.text)
			e.cursor = [2]int{0, tc.cursor}
			p := &utf16CompletionProbe{t: t, wantUnits: jsstring.ToUTF16(tc.text), wantByteCol: tc.byteCol, prefix: tc.prefix}
			e.SetAutocomplete(p)
			e.HandleInput("\t")
			if p.calls != 1 {
				t.Fatalf("provider calls=%d want1", p.calls)
			}
			assertEditorUnits(t, e, tc.want, 1)
		})
	}
}

func TestEditorRemoteTextRetainsUTF16Cursor(t *testing.T) {
	e := NewEditor()
	e.SetRemote(&recordingRemote{})
	e.ApplyRemoteChange("ä😀", "ä😀")
	e.SetRemote(nil)
	e.HandleInput("X")
	assertEditorUnits(t, e, []uint16{'ä', 0xd83d, 0xde00, 'X'}, 4)
}
