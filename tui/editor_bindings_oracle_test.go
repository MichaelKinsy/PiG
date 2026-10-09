package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type editorBindingsOp struct {
	Keys    *string `json:"keys,omitempty"`
	SetText *string `json:"setText,omitempty"`
	History *string `json:"history,omitempty"`
	Render  *int    `json:"render,omitempty"`
}

type editorBindingsProbe struct {
	PaddingX int `json:"paddingX"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Ops      []editorBindingsOp  `json:"ops"`
}

// Pi's Editor resolves every key through the configured keybindings (editor.ts handleInput); the stock fixture
// (editor-oracle.json.gz) only exercises the defaults. These live probes rebind editor, input and select actions, including keys shared by two
// actions, and run seeded random sequences of those keys, text, pastes, setText, history and renders through Pi and Pig.
func TestEditorRebindingsMatchPi(t *testing.T) {
	bindingSets := []map[string][]string{
		{"tui.editor.cursorLineStart": {"ctrl+e"}, "tui.editor.cursorLineEnd": {"ctrl+a"}},
		{"tui.input.submit": {"ctrl+s"}, "tui.input.newLine": {"enter"}},
		{"tui.editor.deleteCharBackward": {"ctrl+h"}, "tui.editor.undo": {"ctrl+z"}, "tui.editor.yank": {"ctrl+v"}},
		{"tui.editor.cursorWordLeft": {"alt+h"}, "tui.editor.cursorWordRight": {"alt+l"}, "tui.editor.deleteWordBackward": {"ctrl+w", "alt+backspace"}},
		{"tui.editor.cursorUp": {"ctrl+p"}, "tui.editor.historyPrevious": {"ctrl+p"}, "tui.editor.cursorDown": {"ctrl+n"}, "tui.editor.historyNext": {"ctrl+n"}},
		{"tui.editor.deleteToLineEnd": {"ctrl+k", "ctrl+x"}, "tui.editor.yankPop": {"ctrl+y"}, "tui.editor.yank": {"ctrl+y"}},
		{"tui.editor.jumpForward": {"ctrl+]"}, "tui.editor.jumpBackward": {"ctrl+alt+]"}, "tui.input.tab": {"ctrl+i"}, "tui.editor.pageUp": {"ctrl+u"}, "tui.editor.pageDown": {"ctrl+d"}},
		{"tui.select.cancel": {"ctrl+g"}, "tui.select.confirm": {"ctrl+s"}, "tui.input.submit": {"enter", "ctrl+s"}, "tui.editor.deleteCharForward": {"ctrl+d"}},
	}
	text := []string{"a", "b", "d", " ", "hello", "wörld", "日本語", "😀", "foo.bar", "/x", "x\\", "\\", "ab cd ef gh ij kl", "e\u0301"}
	keys := []string{
		"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[H", "\x1b[F", "\x01", "\x05", "\x1b[1;5D", "\x1b[1;5C", "\x1bb", "\x1bf", "\x17", "\x1b\x7f", "\x1bd",
		"\x0b", "\x15", "\x19", "\x1by", "\x7f", "\x1b[3~", "\x08", "\x1a", "\x16", "\x18", "\x13", "\x10", "\x0e", "\x04", "\x07", "\x09", "\x1d", "\x1b\x1d",
		"\x1bh", "\x1bl", "\x1b[5~", "\x1b[6~", "\r", "\r", "\n", "\x1b\r", "\x1b[13;2u", "\x1b", "\x03", "\x1b[200~pasted\x1b[201~", "\x1b[200~l1\nl2\x1b[201~", "\x1b[27;2;13~",
	}
	seed := uint32(0xed170042)
	random := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed) / 0x100000000
	}
	pick := func(list []string) string { return list[int(random()*float64(len(list)))] }
	var probes []editorBindingsProbe
	for _, bindings := range bindingSets {
		for range 40 {
			probe := editorBindingsProbe{PaddingX: int(random() * 3), Bindings: bindings}
			for range 8 + int(random()*20) {
				r := random()
				switch {
				case r < 0.03:
					probe.Ops = append(probe.Ops, editorBindingsOp{SetText: new(pick([]string{"", "one\ntwo", "日本語 text\nsecond line", "a\r\nb"}))})
				case r < 0.08:
					probe.Ops = append(probe.Ops, editorBindingsOp{History: new(pick([]string{"first", "multi\nline", "日本語"}))})
				case r < 0.18:
					probe.Ops = append(probe.Ops, editorBindingsOp{Render: new(int(8 + random()*50))})
				case r < 0.5:
					probe.Ops = append(probe.Ops, editorBindingsOp{Keys: new(pick(text))})
				default:
					probe.Ops = append(probe.Ops, editorBindingsOp{Keys: new(pick(keys))})
				}
			}
			probe.Ops = append(probe.Ops, editorBindingsOp{Render: new(40)})
			probes = append(probes, probe)
		}
	}
	// A cursor move ends a kill sequence (editor.ts sets lastAction = null), so the second kill starts a new kill-ring
	// entry and the yank restores only it; random scripts rarely place a move between two kills.
	for _, move := range []string{"\x1b[H", "\x01", "\x1b[D", "\x1b[1;5D"} {
		probe := editorBindingsProbe{Bindings: map[string][]string{}}
		for _, keys := range []string{"ab cd ef", "\x1b[F", "\x17", move, "\x0b", "\x19"} {
			probe.Ops = append(probe.Ops, editorBindingsOp{Keys: new(keys)})
		}
		probe.Ops = append(probe.Ops, editorBindingsOp{Render: new(40)})
		probes = append(probes, probe)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_bindings.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]editorOracleLog
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous, previousKitty := GetKeybindings(), IsKittyProtocolActive()
	// Pi runs without the Kitty keyboard protocol, which changes how LF and other legacy keys match; pin it against a leaked global.
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKeybindings(previous); SetKittyProtocolActive(previousKitty) })
	failures, ops := 0, 0
	for index, probe := range probes {
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		e := NewEditor()
		e.BorderColor = func(s string) string { return s }
		e.SetFocused(true)
		e.SetPaddingX(probe.PaddingX)
		var callbacks [][2]string
		e.OnSubmit = func(text string) { callbacks = append(callbacks, [2]string{"submit", markLoneSurrogatesOracle(text)}) }
		e.OnChange = func(text string) { callbacks = append(callbacks, [2]string{"change", markLoneSurrogatesOracle(text)}) }
		for step, op := range probe.Ops {
			ops++
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
			want := expected[index][step]
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
				if failures <= 8 {
					var prefix []string
					for _, earlier := range probe.Ops[:step+1] {
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
					t.Errorf("probe %d bindings %v step %d\n ops %v\n %s", index, probe.Bindings, step, prefix, diff)
				}
				break
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	t.Logf("%d probes, %d ops", len(probes), ops)
}
