package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type ceOracleOp struct {
	Keys          *string `json:"keys,omitempty"`
	Insert        *string `json:"insert,omitempty"`
	ReplaceAction *string `json:"replaceAction,omitempty"`
	Width         int     `json:"width"`
}

type ceOracleProbe struct {
	PaddingX     int      `json:"paddingX"`
	Actions      []string `json:"actions"`
	OnEscape     bool     `json:"onEscape"`
	OnCtrlD      bool     `json:"onCtrlD"`
	OnPasteImage bool     `json:"onPasteImage"`
	Shortcut     string   `json:"shortcut"`
	// Bindings are user keybindings: several app actions bound to one key make the registration order of the action handlers observable (custom-editor.ts:138-143 runs the first registered match).
	Bindings map[string][]string `json:"bindings,omitempty"`
	Ops      []ceOracleOp        `json:"ops"`
}

type ceOracleFrame struct {
	Calls  []string `json:"calls"`
	Text   string   `json:"text"`
	Cursor struct {
		Line int `json:"line"`
		Col  int `json:"col"`
	} `json:"cursor"`
	Autocomplete bool     `json:"autocomplete"`
	Rows         []string `json:"rows"`
}

type ceOracleProvider struct{}

func (ceOracleProvider) GetSuggestions(_ context.Context, lines []string, cursorLine, cursorCol int, _ tui.AutocompleteSuggestionOptions) *tui.AutocompleteSuggestions {
	before := lines[cursorLine][:cursorCol]
	if !strings.HasPrefix(before, "/") || strings.ContainsAny(before, " \t\n\r") {
		return nil
	}
	var items []tui.AutocompleteItem
	for _, name := range []string{"alpha", "alpine", "beta", "clear", "model"} {
		if strings.HasPrefix(name, before[1:]) {
			items = append(items, tui.AutocompleteItem{Value: name, Label: name})
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &tui.AutocompleteSuggestions{Items: items, Prefix: before}
}

func (ceOracleProvider) ApplyCompletion(lines []string, cursorLine, cursorCol int, item tui.AutocompleteItem, prefix string) ([]string, int, int) {
	line := lines[cursorLine]
	start := max(0, cursorCol-len(prefix))
	out := append([]string{}, lines...)
	out[cursorLine] = line[:start] + "/" + item.Value + " " + line[cursorCol:]
	return out, cursorLine, start + 1 + len(item.Value) + 1
}

func ceOracleProbes() []ceOracleProbe {
	rng := rand.New(rand.NewPCG(17, 2))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	actionNames := []string{"app.interrupt", "app.clear", "app.exit", "app.suspend", "app.thinking.cycle", "app.model.cycleForward", "app.model.cycleBackward", "app.model.select", "app.tools.expand", "app.thinking.toggle", "app.editor.external", "app.message.followUp", "app.message.dequeue", "app.clipboard.pasteImage", "app.session.new", "app.session.tree", "app.session.fork", "app.session.resume"}
	keys := []string{"\x1bz", "\x1b", "\x03", "\x04", "\x1a", "\x1b[Z", "\x0c", "\x10", "\x0e", "\x14", "\x0f", "\x07", "\x16", "\x1b\r", "\x1b[1;3A", "\x1b[1;3B", "\x1b[A", "\x1b[B", "\r", "\t", "\x7f", "\x1b[3~", "a", "b", "/", "\x18", "\x1b[13;2u"}
	// Keys that rewrite the text while the popup is open (kill and delete-word keys), moves the cursor with it open, and multi-character chunks are left out: Pi refreshes the popup from an asynchronous provider after the text changed, so it can show a stale one, as in the editor autocomplete oracle (Q10).
	widths := []int{4, 10, 24, 40, 80}
	var probes []ceOracleProbe
	for range 800 {
		probe := ceOracleProbe{PaddingX: rng.IntN(3), Actions: []string{}, OnEscape: rng.IntN(4) == 0, OnCtrlD: rng.IntN(4) == 0, OnPasteImage: rng.IntN(3) == 0}
		for _, name := range actionNames {
			if rng.IntN(2) == 0 {
				probe.Actions = append(probe.Actions, name)
			}
		}
		rng.Shuffle(len(probe.Actions), func(i, j int) { probe.Actions[i], probe.Actions[j] = probe.Actions[j], probe.Actions[i] })
		if rng.IntN(2) == 0 {
			shared := pick([]string{"ctrl+x", "ctrl+t", "ctrl+o", "ctrl+g", "alt+z"})
			probe.Bindings = map[string][]string{}
			for range 2 + rng.IntN(3) {
				probe.Bindings[pick(actionNames)] = []string{shared}
			}
		}
		if rng.IntN(4) == 0 {
			probe.Shortcut = pick([]string{"\x18", "a", "\x1b[Z"})
		}
		for range 2 + rng.IntN(12) {
			op := ceOracleOp{Width: widths[rng.IntN(len(widths))]}
			switch rng.IntN(10) {
			case 0:
				text := pick([]string{"inserted", "two\nlines", "", "tab\there", "日本語", "\r\n"})
				op.Insert = &text
			case 1:
				name := pick(actionNames)
				if len(probe.Bindings) > 0 && rng.IntN(2) == 0 {
					bound := make([]string, 0, len(probe.Bindings))
					for action := range probe.Bindings {
						bound = append(bound, action)
					}
					slices.Sort(bound)
					name = pick(bound)
				}
				op.ReplaceAction = &name
			default:
				key := pick(keys)
				op.Keys = &key
			}
			probe.Ops = append(probe.Ops, op)
		}
		probes = append(probes, probe)
	}
	return probes
}

func runCeOracleWithPig(probe ceOracleProbe) []ceOracleFrame {
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	user := map[string][]KeyID{}
	for action, keys := range probe.Bindings {
		for _, key := range keys {
			user[action] = append(user[action], KeyID(key))
		}
	}
	keybindings := NewKeybindingsManagerFromBindings(user, "")
	tui.SetKeybindings(tui.GetTUIKeybindings())
	padding := probe.PaddingX
	editor := NewCustomEditor(nil, editorTheme(), keybindings, CustomEditorOptions{PaddingX: &padding})
	editor.SetFocused(true)
	editor.SetAutocomplete(ceOracleProvider{})
	var calls []string
	for _, action := range probe.Actions {
		editor.OnAction(AppKeybinding(action), func() { calls = append(calls, "action:"+action) })
	}
	if probe.OnEscape {
		editor.OnEscape = func() { calls = append(calls, "onEscape") }
	}
	if probe.OnCtrlD {
		editor.OnCtrlD = func() { calls = append(calls, "onCtrlD") }
	}
	if probe.OnPasteImage {
		editor.OnPasteImage = func() { calls = append(calls, "onPasteImage") }
	}
	if probe.Shortcut != "" {
		editor.OnExtensionShortcut = func(data string) bool { calls = append(calls, "shortcut"); return data == probe.Shortcut }
	}
	var frames []ceOracleFrame
	for _, op := range probe.Ops {
		calls = nil
		switch {
		case op.Keys != nil:
			editor.HandleInput(*op.Keys)
		case op.Insert != nil:
			editor.InsertTextAtCursor(*op.Insert)
		case op.ReplaceAction != nil:
			action := *op.ReplaceAction
			editor.OnAction(AppKeybinding(action), func() { calls = append(calls, "replaced:"+action) })
		}
		frame := ceOracleFrame{Calls: append([]string{}, calls...), Text: editor.Text(), Autocomplete: editor.AutocompleteOpen(), Rows: editor.Render(op.Width)}
		cursor := editor.GetCursor()
		frame.Cursor.Line, frame.Cursor.Col = cursor.Line, cursor.Col
		frames = append(frames, frame)
	}
	return frames
}

// CustomEditor (custom-editor.ts) against pinned Pi over 800 seeded key sequences: app actions registered in a random order, replaced by a later registration, the escape, exit and paste-image hooks that override them, an extension shortcut that consumes a key or lets it through, the autocomplete popup deciding whether escape reaches the app, ctrl+d only when the editor is empty, editor history keys taking precedence over app actions, insertTextAtCursor, and the frame after every key; the handlers that ran, the text, the cursor, the popup state and the rows must agree.
func TestCustomEditorMatchesPi(t *testing.T) {
	probes := ceOracleProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/custom_editor.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]ceOracleFrame
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	previousKeys := tui.GetKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetKeybindings(previousKeys)
	})
	failures := 0
	for i, probe := range probes {
		got := runCeOracleWithPig(probe)
		if reflect.DeepEqual(got, expected[i]) {
			continue
		}
		if failures++; failures > 5 {
			continue
		}
		for k := range got {
			if !reflect.DeepEqual(got[k], expected[i][k]) {
				g, _ := json.Marshal(got[k])
				e, _ := json.Marshal(expected[i][k])
				ops, _ := json.Marshal(probe.Ops[:k+1])
				setup, _ := json.Marshal(map[string]any{"actions": probe.Actions, "onEscape": probe.OnEscape, "onCtrlD": probe.OnCtrlD, "onPasteImage": probe.OnPasteImage, "shortcut": probe.Shortcut, "paddingX": probe.PaddingX, "bindings": probe.Bindings})
				t.Errorf("probe %d %s\n  ops %.700s\n  Pig %.900s\n  Pi  %.900s", i, setup, ops, g, e)
				break
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	_ = fmt.Sprint
}
