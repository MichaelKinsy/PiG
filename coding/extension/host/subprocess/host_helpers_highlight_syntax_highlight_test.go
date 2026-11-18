//go:build !pig_strip_syntax_highlight

package subprocess

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestHighlightCodeLinesMatchPiUnderThePalette(t *testing.T) {
	// The host's own theme is not the palette's: the lines come out in the palette's colors only when the call carries it.
	previous := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeInstance(previous) })
	for _, c := range piVectorCases(t, "highlightCode") {
		var want struct {
			Palette json.RawMessage
			Lines   []string
		}
		if err := json.Unmarshal(c.Result, &want); err != nil {
			t.Fatal(err)
		}
		var code string
		var lang *string
		if err := json.Unmarshal(c.Args[0], &code); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(c.Args[1], &lang)
		got, err := HighlightCodeLines(code, derefOrEmpty(lang), want.Palette)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if !reflect.DeepEqual(got, want.Lines) {
			t.Errorf("%s: HighlightCodeLines(%q, %v) = %q\nPi %q", c.Name, code, derefOrEmpty(lang), got, want.Lines)
		}
	}
}

func TestMarkdownHighlightCodeLinesMatchPi(t *testing.T) {
	previous := tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeInstance(previous) })
	for _, c := range piVectorCases(t, "getMarkdownTheme") {
		var want struct {
			Palette   json.RawMessage
			Highlight []struct {
				Code  string
				Lang  any
				Lines []string
			}
		}
		if err := json.Unmarshal(c.Result, &want); err != nil {
			t.Fatal(err)
		}
		for _, h := range want.Highlight {
			lang, _ := h.Lang.(string)
			got, err := HighlightCodeLines(h.Code, lang, want.Palette)
			if err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
			if !reflect.DeepEqual(got, h.Lines) {
				t.Errorf("%s: markdown HighlightCodeLines(%q, %q) = %q\nPi %q", c.Name, h.Code, lang, got, h.Lines)
			}
		}
	}
}
