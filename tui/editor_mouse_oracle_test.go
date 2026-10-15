package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type editorMouseEvent struct {
	Type       string `json:"type"`
	Button     string `json:"button"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	WheelDelta int    `json:"wheelDelta,omitempty"`
	ClickCount int    `json:"clickCount,omitempty"`
}

type editorMouseScenario struct {
	Width    int                `json:"width"`
	PaddingX int                `json:"paddingX"`
	MaxVis   *int               `json:"maxVisible"`
	Commands []acCommand        `json:"commands"`
	Text     string             `json:"text"`
	Keys     []string           `json:"keys"`
	Events   []editorMouseEvent `json:"events"`
}

type editorMouseOut struct {
	Result *mouseOracleResult `json:"result"`
	Text   string             `json:"text"`
	Cursor struct {
		Line int `json:"line"`
		Col  int `json:"col"`
	} `json:"cursor"`
}

type editorMouseRecord struct {
	Frames [][]string       `json:"frames"`
	Outs   []editorMouseOut `json:"outs"`
}

func editorMouseScenarios() []editorMouseScenario {
	rng := rand.New(rand.NewPCG(21, 8))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	names := []string{"alpha", "alpine", "beta", "build", "clear", "compact", "copy", "debug", "diff", "export", "fork", "help", "login", "model", "new", "quit", "reload", "tree", "日本語のコマンド", "x"}
	texts := []string{"", "hello world", "one\ntwo\nthree", "a longer line that must wrap when the editor is narrow enough to need it", "日本語テキスト\nwide chars", "tab\tbed\nline", "e\u0301mbedded combining", "😀 emoji 😀 line\nsecond", strings.Repeat("word ", 30), "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20"}
	var scenarios []editorMouseScenario
	for range 1600 {
		scenario := editorMouseScenario{Width: []int{4, 5, 7, 8, 9, 13, 14, 24, 40, 80}[rng.IntN(10)], PaddingX: rng.IntN(4), Text: pick(texts), Keys: []string{}}
		if scenario.Width < 10 {
			// Pi's wordWrapLine recurses without end for a wide character at a content width of 1 (Q8), so the narrow widths keep a content width of at least 2.
			scenario.PaddingX = min(scenario.PaddingX, 1)
		}
		if rng.IntN(3) == 0 {
			v := 1 + rng.IntN(8)
			scenario.MaxVis = &v
		}
		for range 2 + rng.IntN(len(names)) {
			c := acCommand{Name: pick(names)}
			if rng.IntN(2) == 0 {
				c.Description = pick([]string{"short", "a considerably longer description", "日本語"})
			}
			scenario.Commands = append(scenario.Commands, c)
		}
		if rng.IntN(2) == 0 {
			scenario.Text = ""
			scenario.Keys = append(scenario.Keys, "/")
			for range rng.IntN(3) {
				scenario.Keys = append(scenario.Keys, pick([]string{"a", "b", "c", "d", "l", "\x1b[B", "\x1b[A"}))
			}
		}
		for range 2 + rng.IntN(14) {
			event := editorMouseEvent{Type: pick([]string{"click", "click", "click", "press", "release", "drag", "move", "wheel"}), Button: pick([]string{"left", "left", "left", "right", "none"}), X: rng.IntN(scenario.Width+3) - 1, Y: rng.IntN(12) - 1}
			if event.Type == "wheel" {
				event.WheelDelta = []int{-1, 1, 0}[rng.IntN(3)]
			}
			if event.Type == "click" {
				event.ClickCount = 1 + rng.IntN(2)
			}
			scenario.Events = append(scenario.Events, event)
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func runEditorMouseWithPig(scenario editorMouseScenario) editorMouseRecord {
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	e := NewEditor()
	e.BorderColor = func(text string) string { return ActiveTheme().Fg("borderMuted", text) }
	e.SetFocused(true)
	e.SetPaddingX(scenario.PaddingX)
	if scenario.MaxVis != nil {
		e.SetAutocompleteMaxVisible(*scenario.MaxVis)
	}
	e.SetAutocomplete(acProvider{commands: scenario.Commands})
	if scenario.Text != "" {
		e.SetText(scenario.Text)
	}
	for _, key := range scenario.Keys {
		e.HandleInput(key)
	}
	rows := e.Render(scenario.Width)
	record := editorMouseRecord{Frames: [][]string{rows}}
	for _, event := range scenario.Events {
		result := e.HandleMouse(TuiMouseEvent{Type: TuiMouseEventType(event.Type), Button: TuiMouseButton(event.Button), X: event.X, Y: event.Y, ScreenX: event.X, ScreenY: event.Y, Width: scenario.Width, Height: len(rows), WheelDelta: event.WheelDelta, ClickCount: event.ClickCount})
		out := editorMouseOut{Text: e.Text()}
		cursor := e.GetCursor()
		out.Cursor.Line, out.Cursor.Col = cursor.Line, cursor.Col
		if result != nil {
			flag := func(b bool) *bool {
				if !b {
					return nil
				}
				return &b
			}
			out.Result = &mouseOracleResult{Handled: flag(result.Handled), Capture: flag(result.Capture), Focus: flag(result.Focus), Render: result.Render}
		}
		record.Outs = append(record.Outs, out)
		rows = e.Render(scenario.Width)
		record.Frames = append(record.Frames, rows)
	}
	return record
}

// Editor.handleMouse (components/editor.ts) against pinned Pi over 1600 seeded scenarios: a click on the border, a text row or below the text places the cursor by display column
// (wide and combining characters, wrapped lines, scrolled text, padding 0 to 3), other buttons and event types are left unhandled, and with the slash-command popup open a click
// or wheel on its rows is forwarded to the list (selection, accept) with the padding and height the popup was drawn with; result flags, text, cursor and every frame must agree.
func TestEditorMouseMatchesPi(t *testing.T) {
	scenarios := editorMouseScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_mouse.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []editorMouseRecord
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousTheme, previousKeys := GetCapabilities(), ActiveTheme(), GetKeybindings()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
		SetKeybindings(previousKeys)
	})
	failures := 0
	for i, scenario := range scenarios {
		got := runEditorMouseWithPig(scenario)
		if reflect.DeepEqual(got, expected[i]) {
			continue
		}
		if failures++; failures > 4 {
			continue
		}
		report := fmt.Sprintf("scenario %d (width %d, padding %d, keys %q, text %q)", i, scenario.Width, scenario.PaddingX, scenario.Keys, scenario.Text)
		if !reflect.DeepEqual(got.Frames[0], expected[i].Frames[0]) {
			report += fmt.Sprintf("\n  first frame\n  Pig %q\n  Pi  %q", got.Frames[0], expected[i].Frames[0])
		}
		for k := range got.Outs {
			g, _ := json.Marshal(got.Outs[k])
			e, _ := json.Marshal(expected[i].Outs[k])
			if !bytes.Equal(g, e) {
				ev, _ := json.Marshal(scenario.Events[k])
				report += fmt.Sprintf("\n  event %d %s\n  Pig %s\n  Pi  %s", k, ev, g, e)
				break
			}
			if !reflect.DeepEqual(got.Frames[k+1], expected[i].Frames[k+1]) {
				report += fmt.Sprintf("\n  frame after event %d\n  Pig %q\n  Pi  %q", k, got.Frames[k+1], expected[i].Frames[k+1])
				break
			}
		}
		t.Error(report)
	}
	if failures > 4 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
