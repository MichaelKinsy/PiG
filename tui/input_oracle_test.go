package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

type inputOracleOp struct {
	Keys     *string `json:"keys,omitempty"`
	SetValue *string `json:"setValue,omitempty"`
	Render   *int    `json:"render,omitempty"`
}

type inputOracleProbe struct {
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Ops      []inputOracleOp     `json:"ops"`
	// Unfocused leaves the field without focus, so its row carries no cursor marker.
	Unfocused bool `json:"unfocused,omitempty"`
}

type inputOracleStep struct {
	Value    string      `json:"value"`
	Cursor   int         `json:"cursor"`
	Events   [][2]string `json:"events"`
	Rendered []string    `json:"rendered"`
}

// input.ts handleInput against the pinned pi-tui: bracketed paste (split across chunks, with trailing input), cancel/undo/submit
// (LF always submits), deletion, kill ring and yank, grapheme and word movement, Kitty and modifyOtherKeys printable text and
// C0/C1 rejection, under default and rebound keybindings; value, UTF-16 cursor, submit/escape callbacks and the rendered row per op.
func TestInputHandleInputMatchesPi(t *testing.T) {
	bindingSets := []map[string][]string{
		nil,
		{"tui.editor.cursorLineStart": {"ctrl+e"}, "tui.editor.cursorLineEnd": {"ctrl+a"}},
		{"tui.input.submit": {"ctrl+s"}, "tui.select.cancel": {"ctrl+g"}},
		{"tui.editor.deleteCharBackward": {"ctrl+h"}, "tui.editor.undo": {"ctrl+z"}, "tui.editor.yank": {"ctrl+v"}},
		{"tui.editor.cursorWordLeft": {"alt+h"}, "tui.editor.cursorWordRight": {"alt+l"}, "tui.editor.deleteWordBackward": {"ctrl+w", "alt+backspace"}},
		{"tui.editor.deleteToLineEnd": {"ctrl+k", "ctrl+x"}, "tui.editor.yankPop": {"ctrl+y"}, "tui.editor.yank": {"ctrl+y"}},
		{"tui.editor.undo": {"ctrl+u"}, "tui.editor.deleteToLineStart": {"ctrl+u"}, "tui.input.submit": {"enter", "ctrl+u"}},
	}
	text := []string{"a", "b", "d", " ", "  ", "hello", "wörld", "日本語", "😀", "👍🏽", "e\u0301", "foo.bar", "a-b_c", "ab cd ef gh ij kl", "ก็"}
	keys := []string{
		"\x1b[D", "\x1b[C", "\x1b[H", "\x1b[F", "\x01", "\x05", "\x1b[1;5D", "\x1b[1;5C", "\x1bb", "\x1bf", "\x17", "\x1b\x7f", "\x1bd", "\x0b", "\x15", "\x19", "\x1by",
		"\x7f", "\x7f", "\x08", "\x1b[3~", "\x1b[3;5~", "\x1a", "\x16", "\x18", "\x13", "\x07", "\x1b", "\x03", "\r", "\n", "\x1b\r", "\x00", "\u0085", "\u009f", "\x1f", "a\x01",
		"\x1b[97u", "\x1b[65;2u", "\x1b[27;2;65~", "\x1b[27;5;98~", "\x1b[1;5u", "\x1b[200~pasted\x1b[201~", "\x1b[200~line1\nline2\r\n\x1b[201~tail", "\x1b[200~par", "tial\x1b[201~", "\x1b[200~", "\x1b[201~", "x\x1b[200~y\x1b[201~z",
	}
	seed := uint32(0x1a9c0042)
	random := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed) / 0x100000000
	}
	pick := func(list []string) string { return list[int(random()*float64(len(list)))] }
	var probes []inputOracleProbe
	// Render edge cases the random sequences rarely reach: an unfocused field (no cursor marker), widths too small for the prompt, and values far wider than the
	// viewport with the cursor at the ends and in the middle, in Latin, CJK, emoji and combining text.
	longValues := []string{"", "x", strings.Repeat("abcdefghij", 12), strings.Repeat("日本語のテキスト", 10), strings.Repeat("😀a", 30), strings.Repeat("e\u0301o ", 25), "tab\there and a \x1b[31mstyled\x1b[0m run that is long enough to scroll"}
	moves := [][]string{nil, {"\x1b[H"}, {"\x1b[H", "\x1b[C", "\x1b[C", "\x1b[C"}, {"\x1b[F", "\x1b[D", "\x1b[D", "\x1b[D", "\x1b[D", "\x1b[D"}, {"\x1b[1;5D", "\x1b[1;5D"}}
	for _, unfocused := range []bool{false, true} {
		for _, value := range longValues {
			for _, move := range moves {
				probe := inputOracleProbe{Unfocused: unfocused, Ops: []inputOracleOp{{SetValue: new(value)}}}
				for _, key := range move {
					probe.Ops = append(probe.Ops, inputOracleOp{Keys: new(key)})
				}
				for _, width := range []int{1, 2, 3, 4, 5, 8, 12, 20, 40, 100} {
					probe.Ops = append(probe.Ops, inputOracleOp{Render: new(width)})
				}
				probes = append(probes, probe)
			}
		}
	}
	for _, bindings := range bindingSets {
		for range 40 {
			probe := inputOracleProbe{Bindings: bindings}
			for range 8 + int(random()*24) {
				r := random()
				switch {
				case r < 0.03:
					probe.Ops = append(probe.Ops, inputOracleOp{SetValue: new(pick([]string{"", "hello world", "日本語 text", "a😀b"}))})
				case r < 0.15:
					probe.Ops = append(probe.Ops, inputOracleOp{Render: new(int(4 + random()*40))})
				case r < 0.5:
					probe.Ops = append(probe.Ops, inputOracleOp{Keys: new(pick(text))})
				default:
					probe.Ops = append(probe.Ops, inputOracleOp{Keys: new(pick(keys))})
				}
			}
			probe.Ops = append(probe.Ops, inputOracleOp{Render: new(30)})
			probes = append(probes, probe)
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/input_component.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]inputOracleStep
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
		field := NewInput(InputOptions{})
		field.Focused = !probe.Unfocused
		var events [][2]string
		field.OnSubmit = func(v string) { events = append(events, [2]string{"submit", markLoneSurrogatesOracle(v)}) }
		field.OnEscape = func() { events = append(events, [2]string{"escape", ""}) }
		for step, op := range probe.Ops {
			ops++
			events = [][2]string{}
			var rendered []string
			switch {
			case op.Keys != nil:
				field.HandleInput(jsstring.FromUTF8([]byte(*op.Keys)))
			case op.SetValue != nil:
				field.SetValue(jsstring.FromUTF8([]byte(*op.SetValue)))
			case op.Render != nil:
				rendered = field.Render(*op.Render)
				for i := range rendered {
					rendered[i] = markLoneSurrogatesOracle(rendered[i])
				}
			}
			want := expected[index][step]
			var diff string
			switch {
			case markLoneSurrogatesOracle(field.GetValue()) != want.Value:
				diff = fmt.Sprintf("value %q want %q", field.GetValue(), want.Value)
			case field.cursor != want.Cursor:
				diff = fmt.Sprintf("cursor %d want %d", field.cursor, want.Cursor)
			case !reflect.DeepEqual(events, want.Events):
				diff = fmt.Sprintf("events %q want %q", events, want.Events)
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
						case earlier.SetValue != nil:
							prefix = append(prefix, fmt.Sprintf("setValue:%q", *earlier.SetValue))
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
