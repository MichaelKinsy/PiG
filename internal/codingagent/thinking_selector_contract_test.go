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

// thinking-selector.ts handleInput boundaries against pinned Pi: no available level, one level, a query that matches no level (the list is empty, so save,
// confirm and cancel have no highlighted row to act on, and up/down do nothing), the highlight surviving a filter rebuild and a cleared query, the save
// key typed into the search when no save callback exists, and page, home and end keys reaching the search Input instead of the list.
func TestThinkingSelectorBoundariesMatchPi(t *testing.T) {
	restoreStartupTheme(t)
	all := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	up, down, enter, esc, save, backspace := "\x1b[A", "\x1b[B", "\r", "\x1b", "\x13", "\x7f"
	cases := []thinkingProbe{
		{Current: "off", Levels: []string{}, Save: true, Keys: []string{save, up, down, enter, "\n", "x", save, enter, esc}},
		{Current: "off", Levels: []string{}, Save: false, Keys: []string{save, "x", up, enter, esc}},
		{Current: "off", Levels: []string{"off"}, Save: true, Keys: []string{up, down, save, enter, esc}},
		{Current: "medium", Levels: []string{"medium"}, Save: true, Keys: []string{down, down, up, up, enter}},
		{Current: "high", Levels: []string{"off", "high"}, Save: true, Keys: []string{up, up, down, down, down, enter}},
		{Current: "off", Levels: all, Save: true, Keys: []string{"z", "z", save, enter, "\n", esc, up, down, save}},
		{Current: "off", Levels: all, Save: true, Keys: []string{"z", "z", backspace, backspace, save, enter}},
		{Current: "medium", Levels: all, Save: true, Keys: []string{down, down, "e", save, "m", backspace, backspace, save, enter}},
		{Current: "low", Levels: all, Save: true, Keys: []string{down, "h", "i", save, backspace, backspace, save, up, save}},
		{Current: "off", Levels: all, Save: true, Keys: []string{down, down, "x", save, enter}},
		{Current: "off", Levels: all, Save: false, Keys: []string{down, "\x1b[6~", "\x1b[5~", "\x1b[H", "\x1b[F", enter}},
		{Current: "off", Levels: all, Save: true, Keys: []string{"a", "\x1b[D", "\x1b[C", "\x01", "\x05", "\x17", "\x15", enter}},
		{Current: "xhigh", Levels: all, Save: true, Keys: []string{down, down, down, up, up, up, up, up, up, up, enter}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"tui.select.up": {"k"}, "tui.select.down": {"j"}}, Keys: []string{"j", "j", "k", enter, "j", save}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"app.thinking.save": {"x"}}, Keys: []string{down, "x", "x", enter}},
		{Current: "off", Levels: all, Save: false, Bindings: map[string][]string{"app.thinking.save": {"x"}}, Keys: []string{down, "x", "x", enter}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"tui.select.cancel": {"ctrl+x"}}, Keys: []string{esc, "\x18", "\x03"}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"tui.select.confirm": {"ctrl+y"}}, Keys: []string{down, enter, "\x19"}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"app.thinking.save": {"escape"}}, Keys: []string{esc, down, esc}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"app.thinking.save": {"enter"}}, Keys: []string{down, enter, "\n"}},
		{Current: "off", Levels: all, Save: false, Bindings: map[string][]string{"app.thinking.save": {"enter"}}, Keys: []string{down, enter}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"app.thinking.save": {"up"}}, Keys: []string{down, up, up}},
		{Current: "off", Levels: all, Save: true, Bindings: map[string][]string{"tui.input.submit": {"ctrl+s"}}, Keys: []string{down, "\x13", "\r"}},
	}
	var probes []thinkingProbe
	for _, theme := range []string{"dark", "light"} {
		for _, probe := range cases {
			probe.Theme, probe.Width = theme, 70
			probes = append(probes, probe)
		}
	}
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
		goLevels := []ai.ThinkingLevel{}
		for _, level := range probe.Levels {
			goLevels = append(goLevels, ai.ThinkingLevel(level))
		}
		selector := NewThinkingSelectorComponent(ai.ThinkingLevel(probe.Current), goLevels,
			func(level ai.ThinkingLevel) { events = append(events, "select:"+string(level)) },
			func() { events = append(events, "cancel") }, save, ai.ThinkingLevel(probe.Default))
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
			t.Errorf("probe %d %+v:\n%s", i, probe, firstFrameDifference(got.Frames, expected[i].Frames))
		}
	}
}
