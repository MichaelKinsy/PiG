package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// editorHandleInputActions are the keybinding actions Editor.handleInput resolves without an autocomplete popup, in the order the generated
// inventory lists them (test/parity/interfaces/behavior-inputs-v1.5.0.json, Editor.handleInput bindings).
var editorHandleInputActions = []string{
	"tui.editor.cursorDown", "tui.editor.cursorLeft", "tui.editor.cursorLineEnd", "tui.editor.cursorLineStart", "tui.editor.cursorRight",
	"tui.editor.cursorUp", "tui.editor.cursorWordLeft", "tui.editor.cursorWordRight", "tui.editor.deleteCharBackward", "tui.editor.deleteCharForward",
	"tui.editor.deleteToLineEnd", "tui.editor.deleteToLineStart", "tui.editor.deleteWordBackward", "tui.editor.deleteWordForward",
	"tui.editor.historyNext", "tui.editor.historyPrevious", "tui.editor.jumpBackward", "tui.editor.jumpForward", "tui.editor.pageDown",
	"tui.editor.pageUp", "tui.editor.undo", "tui.editor.yank", "tui.editor.yankPop",
	"tui.input.copy", "tui.input.newLine", "tui.input.submit", "tui.input.tab",
}

// editorSharedKeyStates are editor situations in which a shared key is observable: each runs the key sequence that builds the state, then the shared key.
// A kill ring entry, a yank, a history entry, a browsed history entry, a backslash before the cursor, a pending jump and a multi-line buffer with the cursor in the middle are each prepared.
var editorSharedKeyStates = [][]string{
	{"\x1b[A"},
	{"ab cd ef", "\x17", "\x19"},
	{"ab cd ef", "\x17", "\x0b", "\x19"},
	{"one\x1b\rtwo\x1b\rthree", "\x1b[A", "\x1b[D"},
	{"ab\\"},
	{"abc", "\x1d"},
	{"abc", "\x1b\x1d"},
	{"x", "\x1b[A"},
	{"hello world", "\x01", "\x1bf"},
	{"hello world", "\x1b[D", "\x1b[D"},
}

// TestEditorSharedKeysResolveInPiOrder binds every pair of handleInput actions to one key and sends that key in each prepared editor state.
// editor.ts checks the actions in one fixed order, so the first action in that order wins; Pi's Editor is the oracle for the winner.
func TestEditorSharedKeysResolveInPiOrder(t *testing.T) {
	sharedKeys := []struct{ id, data string }{{"ctrl+q", "\x11"}, {"enter", "\r"}, {"tab", "\t"}, {"up", "\x1b[A"}, {"backspace", "\x7f"}}
	var probes []editorBindingsProbe
	for _, shared := range sharedKeys {
		for first := range editorHandleInputActions {
			for second := first + 1; second < len(editorHandleInputActions); second++ {
				bindings := map[string][]string{editorHandleInputActions[first]: {shared.id}, editorHandleInputActions[second]: {shared.id}}
				for _, state := range editorSharedKeyStates {
					probe := editorBindingsProbe{Bindings: bindings}
					probe.Ops = append(probe.Ops, editorBindingsOp{History: new("recalled")}, editorBindingsOp{History: new("two\nlines")})
					for _, keys := range state {
						probe.Ops = append(probe.Ops, editorBindingsOp{Keys: new(keys)})
					}
					probe.Ops = append(probe.Ops, editorBindingsOp{Keys: new(shared.data)}, editorBindingsOp{Render: new(30)},
						editorBindingsOp{Keys: new("z")}, editorBindingsOp{Keys: new(shared.data)}, editorBindingsOp{Render: new(30)})
					probes = append(probes, probe)
				}
			}
		}
	}
	compareEditorProbesWithPi(t, probes)
}

type editorAutocompleteBindingsOp struct {
	Keys    *string `json:"keys,omitempty"`
	SetText *string `json:"setText,omitempty"`
	History *string `json:"history,omitempty"`
}

type editorAutocompleteBindingsProbe struct {
	DisableSubmit bool `json:"disableSubmit,omitempty"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string            `json:"bindings,omitempty"`
	Ops      []editorAutocompleteBindingsOp `json:"ops"`
}

type editorAutocompleteBindingsLog struct {
	Text      string      `json:"text"`
	Cursor    struct{ Line, Col int }
	Lines     []string    `json:"lines"`
	Callbacks [][2]string `json:"callbacks"`
	Rows      []string    `json:"rows"`
}

// editorAutocompleteBindingsProvider is the provider testdata/editor_autocomplete_bindings.mjs defines for Pi: "/" followed by non-space characters offers the commands with that prefix;
// otherwise only a forced request (Tab) offers the file names starting with the word before the cursor.
type editorAutocompleteBindingsProvider struct{}

var (
	editorAutocompleteCommands = []string{"alpha", "alpine", "beta", "build", "clear"}
	editorAutocompleteFiles    = []string{"alpha.txt", "alpine.go", "beta.md"}
)

func (editorAutocompleteBindingsProvider) GetSuggestions(_ context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteSuggestionOptions) *AutocompleteSuggestions {
	before := lines[cursorLine][:cursorCol]
	if strings.HasPrefix(before, "/") && !strings.ContainsAny(before, " \t") {
		var items []AutocompleteItem
		for _, name := range editorAutocompleteCommands {
			if strings.HasPrefix(name, before[1:]) {
				items = append(items, AutocompleteItem{Value: name, Label: name})
			}
		}
		if len(items) == 0 {
			return nil
		}
		return &AutocompleteSuggestions{Items: items, Prefix: before}
	}
	if !options.Force {
		return nil
	}
	token := before[strings.LastIndexAny(before, " \t")+1:]
	var items []AutocompleteItem
	for _, name := range editorAutocompleteFiles {
		if strings.HasPrefix(name, token) {
			items = append(items, AutocompleteItem{Value: name, Label: name})
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &AutocompleteSuggestions{Items: items, Prefix: token}
}

func (editorAutocompleteBindingsProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item AutocompleteItem, prefix string) ([]string, int, int) {
	line := lines[cursorLine]
	start := max(0, cursorCol-len(prefix))
	insert := item.Value
	if strings.HasPrefix(prefix, "/") {
		insert = "/" + item.Value + " "
	}
	out := append([]string{}, lines...)
	out[cursorLine] = line[:start] + insert + line[cursorCol:]
	return out, cursorLine, start + len(insert)
}

func pigEditorAutocompleteLog(probe editorAutocompleteBindingsProbe) []editorAutocompleteBindingsLog {
	previous, previousKitty, previousCaps, previousTheme := GetKeybindings(), IsKittyProtocolActive(), GetCapabilities(), ActiveTheme()
	SetKittyProtocolActive(false)
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	defer func() {
		SetKeybindings(previous)
		SetKittyProtocolActive(previousKitty)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	}()
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
	e := NewEditor()
	e.BorderColor = func(text string) string { return ActiveTheme().Fg("borderMuted", text) }
	e.SetFocused(true)
	e.DisableSubmit = probe.DisableSubmit
	e.SetAutocompleteMaxVisible(5)
	e.SetAutocomplete(editorAutocompleteBindingsProvider{})
	var callbacks [][2]string
	e.OnSubmit = func(text string) { callbacks = append(callbacks, [2]string{"submit", text}) }
	e.OnChange = func(text string) { callbacks = append(callbacks, [2]string{"change", text}) }
	log := make([]editorAutocompleteBindingsLog, 0, len(probe.Ops))
	for _, op := range probe.Ops {
		callbacks = nil
		switch {
		case op.Keys != nil:
			e.HandleInput(*op.Keys)
		case op.SetText != nil:
			e.SetText(*op.SetText)
		case op.History != nil:
			e.AddToHistory(*op.History)
		}
		entry := editorAutocompleteBindingsLog{Text: e.Text(), Lines: e.GetLines(), Callbacks: callbacks, Rows: e.Render(40)}
		cursor := e.GetCursor()
		entry.Cursor.Line, entry.Cursor.Col = cursor.Line, cursor.Col
		log = append(log, entry)
	}
	return log
}

// compareEditorAutocompleteProbesWithPi runs each probe through Pi's Editor and Pig's with the same provider and requires every op's text, cursor, lines, callbacks and rendered rows (popup included) to agree.
func compareEditorAutocompleteProbesWithPi(t *testing.T, probes []editorAutocompleteBindingsProbe) {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_autocomplete_bindings.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]editorAutocompleteBindingsLog
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures, popups := 0, 0
	for index, probe := range probes {
		got := pigEditorAutocompleteLog(probe)
		for step := range probe.Ops {
			want, have := expected[index][step], got[step]
			if len(have.Rows) > 3 {
				popups++
			}
			if reflect.DeepEqual(want, normalizeAutocompleteLog(have)) {
				continue
			}
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
					}
				}
				t.Errorf("probe %d bindings %v step %d disableSubmit=%v\n ops %v\n Pig %+v\n Pi  %+v", index, probe.Bindings, step, probe.DisableSubmit, prefix, have, want)
			}
			break
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if popups == 0 {
		t.Errorf("no probe opened a popup")
	}
	t.Logf("%d probes, %d popup frames", len(probes), popups)
}

// normalizeAutocompleteLog gives an absent callback list the shape the oracle decodes: an empty slice.
func normalizeAutocompleteLog(entry editorAutocompleteBindingsLog) editorAutocompleteBindingsLog {
	if entry.Callbacks == nil {
		entry.Callbacks = [][2]string{}
	}
	return entry
}

// TestEditorAutocompleteKeysResolveInPiOrder binds each autocomplete selection action and each handleInput action to one shared key and sends it with a slash popup open, with a forced file popup open and with no popup.
func TestEditorAutocompleteKeysResolveInPiOrder(t *testing.T) {
	selectActions := []string{"tui.select.cancel", "tui.select.up", "tui.select.down", "tui.select.pageUp", "tui.select.pageDown", "tui.select.confirm"}
	others := append(append([]string{}, selectActions...), editorHandleInputActions...)
	states := [][]string{
		{"/", "a"},
		{"/", "a", "\x1b[B"},
		{"/"},
		{"x al", "\t"},
		{"x al", "\t", "\x1b[B"},
		{"x"},
	}
	var probes []editorAutocompleteBindingsProbe
	for _, shared := range []struct{ id, data string }{{"ctrl+q", "\x11"}, {"enter", "\r"}, {"tab", "\t"}, {"up", "\x1b[A"}, {"escape", "\x1b"}} {
		for first, action := range selectActions {
			for _, other := range others[first+1:] {
				for _, disabled := range []bool{false, true} {
					for _, state := range states {
						probe := editorAutocompleteBindingsProbe{Bindings: map[string][]string{action: {shared.id}, other: {shared.id}}, DisableSubmit: disabled}
						probe.Ops = append(probe.Ops, editorAutocompleteBindingsOp{History: new("recalled")})
						for _, keys := range state {
							probe.Ops = append(probe.Ops, editorAutocompleteBindingsOp{Keys: new(keys)})
						}
						probe.Ops = append(probe.Ops, editorAutocompleteBindingsOp{Keys: new(shared.data)}, editorAutocompleteBindingsOp{Keys: new("z")}, editorAutocompleteBindingsOp{Keys: new(shared.data)})
						probes = append(probes, probe)
					}
				}
			}
		}
	}
	compareEditorAutocompleteProbesWithPi(t, probes)
}

// TestEditorAutocompleteSequencesMatchPi runs seeded random key sequences with the slash and forced file providers under default and rebound selection keys, through Pi's Editor and Pig's.
func TestEditorAutocompleteSequencesMatchPi(t *testing.T) {
	bindingSets := []map[string][]string{
		nil,
		{"tui.select.cancel": {"ctrl+g"}, "tui.select.confirm": {"ctrl+s"}, "tui.input.submit": {"enter", "ctrl+s"}},
		{"tui.select.up": {"ctrl+p"}, "tui.select.down": {"ctrl+n"}, "tui.editor.cursorUp": {"ctrl+p"}, "tui.editor.cursorDown": {"ctrl+n"}},
		{"tui.select.pageUp": {"ctrl+u"}, "tui.select.pageDown": {"ctrl+d"}, "tui.editor.pageUp": {"ctrl+u"}, "tui.editor.pageDown": {"ctrl+d"}},
		{"tui.input.tab": {"ctrl+i", "ctrl+t"}, "tui.editor.undo": {"ctrl+t"}, "tui.select.cancel": {"escape", "ctrl+t"}},
	}
	keys := []string{
		"/", "a", "l", "b", " ", "x", "\t", "\x1b", "\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[5~", "\x1b[6~", "\r", "\n", "\x7f", "\x1b[3~", "\x01", "\x05", "\x0b", "\x15",
		"\x1a", "\x17", "\x19", "\x1by", "\x1b[H", "\x1b[F", "\x1b[1;5D", "\x1b[1;5C", "\x07", "\x13", "\x10", "\x0e", "\x14", "\x1f", "\x1b[200~/al\x1b[201~", "\x1b\r",
	}
	seed := uint32(0xac0017)
	random := func() float64 {
		seed = seed*1664525 + 1013904223
		return float64(seed) / 0x100000000
	}
	pick := func(list []string) string { return list[int(random()*float64(len(list)))] }
	var probes []editorAutocompleteBindingsProbe
	for _, bindings := range bindingSets {
		for range 200 {
			probe := editorAutocompleteBindingsProbe{Bindings: bindings, DisableSubmit: random() < 0.1}
			for range 6 + int(random()*20) {
				switch r := random(); {
				case r < 0.03:
					probe.Ops = append(probe.Ops, editorAutocompleteBindingsOp{SetText: new(pick([]string{"", "/a", "one\ntwo"}))})
				case r < 0.06:
					probe.Ops = append(probe.Ops, editorAutocompleteBindingsOp{History: new(pick([]string{"first", "/multi\nline"}))})
				default:
					probe.Ops = append(probe.Ops, editorAutocompleteBindingsOp{Keys: new(pick(keys))})
				}
			}
			probes = append(probes, probe)
		}
	}
	compareEditorAutocompleteProbesWithPi(t, probes)
}
