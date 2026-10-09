package tui

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func markLoneSurrogatesOracle(text string) string {
	units := jsstring.ToUTF16(text)
	var out strings.Builder
	for index := 0; index < len(units); index++ {
		unit := units[index]
		switch {
		case unit >= 0xd800 && unit <= 0xdbff && index+1 < len(units) && units[index+1] >= 0xdc00 && units[index+1] <= 0xdfff:
			out.WriteString(string(utf16.DecodeRune(rune(unit), rune(units[index+1]))))
			index++
		case unit >= 0xd800 && unit <= 0xdfff:
			fmt.Fprintf(&out, "\uf8ff%x", unit)
		default:
			out.WriteRune(rune(unit))
		}
	}
	return out.String()
}

type editorOracleOp struct {
	Keys    *string `json:"keys"`
	SetText *string `json:"setText"`
	History *string `json:"history"`
	Render  *int    `json:"render"`
}

type editorOracleLog struct {
	Text      string
	Expanded  string
	Cursor    struct{ Line, Col int }
	Lines     []string
	Callbacks [][2]string
	Rendered  []string
}

// The fixture is Pi 1.0.4's Editor run over seeded random key sequences (testdata/editor-oracle.mjs): typing text with CJK,
// emoji, combining marks and Thai, arrows, word and line movement, kill ring and yank pop, undo, history, backslash newline,
// CSI-u and modifyOtherKeys input, character jump, page keys, bracketed paste (short, multi-line, large and split), setText,
// and render at several widths with padding. Each op compares text, expanded text, cursor, lines, callbacks and rendered lines.
func TestEditorMatchesPiOracle(t *testing.T) {
	// Pi's editor resolves keys through getKeybindings(), which defaults to TUI_KEYBINDINGS on every platform.
	// Install that default so a registry another test left behind cannot change the result.
	previousKeybindings := GetKeybindings()
	SetKeybindings(NewTUIKeybindingsManager(nil))
	t.Cleanup(func() { SetKeybindings(previousKeybindings) })
	file, err := os.Open("testdata/editor-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Options struct{ PaddingX int }
		Ops     []editorOracleOp
		Log     []editorOracleLog
	}
	if err := json.NewDecoder(zr).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for index, c := range cases {
		e := NewEditor()
		e.BorderColor = func(s string) string { return s }
		e.SetFocused(true)
		e.SetPaddingX(c.Options.PaddingX)
		var callbacks [][2]string
		e.OnSubmit = func(text string) { callbacks = append(callbacks, [2]string{"submit", markLoneSurrogatesOracle(text)}) }
		e.OnChange = func(text string) { callbacks = append(callbacks, [2]string{"change", markLoneSurrogatesOracle(text)}) }
		for step, op := range c.Ops {
			callbacks = nil
			var rendered []string
			switch {
			case op.Keys != nil:
				e.HandleInput(jsstring.FromUTF8([]byte(*op.Keys)))
			case op.SetText != nil:
				e.SetText(jsstring.FromUTF8([]byte(*op.SetText)))
			case op.History != nil:
				e.AddToHistory(jsstring.FromUTF8([]byte(*op.History)))
			case op.Render != nil:
				rendered = e.Render(*op.Render)
				for i := range rendered {
					rendered[i] = markLoneSurrogatesOracle(rendered[i])
				}
			}
			lines := e.GetLines()
			for i := range lines {
				lines[i] = markLoneSurrogatesOracle(lines[i])
			}
			cursor := e.GetCursor()
			want := c.Log[step]
			var diff string
			switch {
			case markLoneSurrogatesOracle(e.Text()) != want.Text:
				diff = fmt.Sprintf("text %q want %q", e.Text(), want.Text)
			case markLoneSurrogatesOracle(e.GetExpandedText()) != want.Expanded:
				diff = fmt.Sprintf("expanded %q want %q", e.GetExpandedText(), want.Expanded)
			case cursor.Line != want.Cursor.Line || cursor.Col != want.Cursor.Col:
				diff = fmt.Sprintf("cursor %v want %v", cursor, want.Cursor)
			case !reflect.DeepEqual(lines, want.Lines):
				diff = fmt.Sprintf("lines %q want %q", lines, want.Lines)
			case !reflect.DeepEqual(callbacks, want.Callbacks) && (len(callbacks) != 0 || len(want.Callbacks) != 0):
				diff = fmt.Sprintf("callbacks %q want %q", callbacks, want.Callbacks)
			case op.Render != nil && !reflect.DeepEqual(rendered, want.Rendered):
				diff = fmt.Sprintf("render %q want %q", rendered, want.Rendered)
			}
			if diff != "" {
				failures++
				if failures <= 10 {
					prefix := make([]string, 0, step+1)
					for _, earlier := range c.Ops[:step+1] {
						switch {
						case earlier.Keys != nil:
							prefix = append(prefix, fmt.Sprintf("keys:%q", *earlier.Keys))
						case earlier.SetText != nil:
							prefix = append(prefix, fmt.Sprintf("setText:%q", *earlier.SetText))
						case earlier.History != nil:
							prefix = append(prefix, fmt.Sprintf("history:%q", *earlier.History))
						case earlier.Render != nil:
							prefix = append(prefix, fmt.Sprintf("render:%d", *earlier.Render))
						}
					}
					t.Errorf("case %d (paddingX %d) step %d\n ops %s\n %s", index, c.Options.PaddingX, step, strings.Join(prefix, " "), diff)
				}
				break
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d sequences differ from Pi", failures, len(cases))
	}
}
