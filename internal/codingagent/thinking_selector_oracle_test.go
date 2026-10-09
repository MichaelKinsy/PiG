package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type thinkingProbe struct {
	Theme   string   `json:"theme"`
	Width   int      `json:"width"`
	Current string   `json:"current"`
	Levels  []string `json:"levels"`
	Save    bool     `json:"save"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type thinkingResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// thinking-selector.ts handleInput against pinned Pi: the save binding (only with an onSelectAsDefault callback) saves the
// highlighted level, up/down/confirm/cancel drive the list (wrapping), and every other key edits the search whose Input submit
// (LF or tui.input.submit) selects, then the list is rebuilt around the previously highlighted level.
func TestThinkingSelectorInputMatchesPi(t *testing.T) {
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	scripts := [][]string{
		{"\x1b[A"},
		{"\x1b[B", "\x1b[B", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\r"},
		{"h", "i", "\x1b[B", "\r"},
		{"h", "\x1b[B", "x", "\x7f", "\r"},
		{"zz", "\r"},
		{"zz", "\x1b[B", "\x13"},
		{"\n"},
		{"\x1b[B", "\n"},
		{"\x1b[B", "\x1b[5~", "\x1b[H", "\r"},
		{"\x1b"},
		{"\x03"},
		{"\x13"},
		{"\x1b[B", "\x13", "\x1b[B", "\x13"},
		{"m", "\x1b[D", "x", "\x17", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{"app.thinking.save": {"ctrl+y"}},
		{"tui.select.confirm": {"ctrl+s"}},
		{"tui.select.cancel": {"ctrl+x"}},
		{"tui.input.submit": {"ctrl+s"}},
		{"tui.select.down": {"ctrl+s"}, "app.thinking.save": {"ctrl+s"}},
	}
	var probes []thinkingProbe
	for _, theme := range []string{"dark", "light"} {
		for _, current := range []string{"off", "medium"} {
			for _, save := range []bool{true, false} {
				for _, b := range bindings {
					for _, keys := range scripts {
						probes = append(probes, thinkingProbe{Theme: theme, Width: 70, Current: current, Levels: levels, Save: save, Bindings: b, Keys: keys})
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/thinking_selector.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []thinkingResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps := tui.ActiveTheme(), tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
	})
	failures := 0
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		tui.SetTheme(probe.Theme)
		bindingMap := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				bindingMap[action] = append(bindingMap[action], KeyID(key))
			}
		}
		useKeybindings(t, bindingMap)
		var events []string
		var save func(ai.ThinkingLevel)
		if probe.Save {
			save = func(level ai.ThinkingLevel) { events = append(events, "save:"+string(level)) }
		}
		var goLevels []ai.ThinkingLevel
		for _, level := range probe.Levels {
			goLevels = append(goLevels, ai.ThinkingLevel(level))
		}
		selector := NewThinkingSelectorComponent(ai.ThinkingLevel(probe.Current), goLevels,
			func(level ai.ThinkingLevel) { events = append(events, "select:"+string(level)) },
			func() { events = append(events, "cancel") }, save, "")
		got := thinkingResult{Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			selector.HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) {
			t.Errorf("probe %d %+v: events = %q; Pi = %q", i, probe, got.Steps, expected[i].Steps)
		}
		if !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			if failures++; failures <= 4 {
				t.Errorf("probe %d %+v:\n%s", i, probe, firstFrameDifference(got.Frames, expected[i].Frames))
			}
		}
	}
}
