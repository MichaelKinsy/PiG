package codingagent

import (
	"bytes"
	"encoding/json"
	"math/rand"
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
	// Default is the defaultThinkingLevel the selector annotates ("· default", thinking-selector.ts:72); empty passes none.
	Default string `json:"default,omitempty"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type thinkingResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// randomThinkingProbes draws a subset of the levels in order, a current and a default level (or none), widths from 8 to 100, bindings and key
// scripts mixing list keys, search text, cursor and word editing, paste, and the save key. PageUp and PageDown stay out: Pi 1.1.0 has no select
// page keys and Pig pages the selection (D155, Pi PR #7680).
func randomThinkingProbes(bindings []map[string][]string) []thinkingProbe {
	rng := rand.New(rand.NewSource(20261028))
	all := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	keys := []string{"\x1b[A", "\x1b[B", "\r", "\n", "\x1b", "\x03", "\x13", "\x7f", "\x1b[3~", "\x1b[D", "\x1b[C", "\x01", "\x05", "\x17", "\x15", "h", "i", "m", "o", "x", "l", "e", "é", "日", " ", "\x1b[200~med\x1b[201~", "\x1b[1;5D", "\x1b[H", "\x1b[F", "\t"}
	var probes []thinkingProbe
	for range 400 {
		var levels []string
		for _, level := range all {
			if rng.Intn(4) != 0 {
				levels = append(levels, level)
			}
		}
		if len(levels) == 0 {
			levels = []string{"off"}
		}
		probe := thinkingProbe{Theme: []string{"dark", "light"}[rng.Intn(2)], Width: 2 + rng.Intn(99), Current: all[rng.Intn(len(all))], Levels: levels, Save: rng.Intn(2) == 0, Bindings: bindings[rng.Intn(len(bindings))]}
		if rng.Intn(2) == 0 {
			probe.Default = all[rng.Intn(len(all))]
		}
		probe.Keys = make([]string, 1+rng.Intn(14))
		for i := range probe.Keys {
			probe.Keys[i] = keys[rng.Intn(len(keys))]
		}
		probes = append(probes, probe)
	}
	return probes
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
	for _, def := range []string{"off", "medium", "xhigh"} {
		for _, width := range []int{70, 30} {
			for _, keys := range scripts[:6] {
				probes = append(probes, thinkingProbe{Theme: "dark", Width: width, Current: "medium", Levels: levels, Save: true, Default: def, Keys: keys})
			}
		}
	}
	probes = append(probes, randomThinkingProbes(bindings)...)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/thinking_selector.mjs", pigversion.UpstreamPackageVersion)
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
			func() { events = append(events, "cancel") }, save, ai.ThinkingLevel(probe.Default))
		frame := func(selector *ThinkingSelectorComponent) []string { return selector.Render(probe.Width) }
		got := thinkingResult{Frames: [][]string{frame(selector)}}
		for _, key := range probe.Keys {
			selector.HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, frame(selector))
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
