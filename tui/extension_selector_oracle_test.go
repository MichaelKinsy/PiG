package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type extensionSelectorProbe struct {
	Theme       string              `json:"theme"`
	Width       int                 `json:"width"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Options     []string            `json:"options"`
	Bindings    map[string][]string `json:"bindings,omitempty"`
	Keys        []string            `json:"keys"`
	Kitty       bool                `json:"kitty,omitempty"`
}

// extension-selector.ts (ExtensionSelectorComponent) against pinned Pi: every frame and the select and cancel callbacks after each key, with
// default and rebound select keys, empty and wide options, and a description. Not compared: app.tools.expand (the tui-only key table has no
// app actions; covered where the interactive mode installs the merged table), the select page keys (D155), the countdown and the preview column
// (no Pi counterpart in the constructor path).
func TestExtensionSelectorMatchesPi(t *testing.T) {
	scripts := [][]string{
		nil,
		{"\x1b[A"},
		{"\x1b[B"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\r"},
		{"\x1b[B", "\r"},
		{"\n"},
		{"\x1b"},
		{"\x03"},
		{"j", "j", "k", "\r"},
		{"k", "k", "\n"},
		{"y", "\x1b[H", "\x1b[F", "\r"},
		{"\x1b[B", "x", "\x1b[B", "\r"},
		{"\x13", "\r"},
		{"\x0e", "\x10", "\r"},
		{"\x18"},
		{"\x1b[B", "\x1b"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.confirm": {"ctrl+s"}},
		{"tui.select.cancel": {"ctrl+x"}},
		{"tui.select.down": {"ctrl+n", "j"}, "tui.select.up": {"ctrl+p"}},
		{"tui.select.confirm": {}, "tui.select.cancel": {}},
	}
	optionSets := [][]string{
		{},
		{"Yes"},
		{"Yes", "No"},
		{"alpha", "beta", "gamma", "delta"},
		{"界🙂 wide option", "a very long option that is longer than the narrowest widths used here and wraps", "\x1b[31mred\x1b[0m", ""},
		{"", "after empty"},
	}
	titles := []string{"Pick one", "界🙂 title that is long enough to wrap at narrow widths", ""}
	descriptions := []string{"", "Describe it", "multi\nline description"}
	var probes []extensionSelectorProbe
	for _, theme := range []string{"dark", "light"} {
		for oi, options := range optionSets {
			for ti, title := range titles {
				for bi, b := range bindings {
					for si, keys := range scripts {
						if (ti+bi+si+oi)%3 != 0 { // a spread of the cross product keeps the probe count bounded
							continue
						}
						for _, width := range []int{70, 24, 9, 4} {
							probes = append(probes, extensionSelectorProbe{Theme: theme, Width: width, Title: title, Description: descriptions[(oi+si)%len(descriptions)], Options: options, Bindings: b, Keys: keys})
						}
					}
				}
			}
		}
	}
	// The Kitty keyboard protocol: CSI-u forms of enter, escape, ctrl+j, ctrl+c, shift+enter and plain letters, which the legacy table reads differently (LF is ctrl+j, not enter).
	for _, keys := range [][]string{{"\x1b[13u"}, {"\x1b[27u"}, {"\x1b[106;5u"}, {"\x1b[99;5u"}, {"\x1b[13;2u"}, {"\n"}, {"\r"}, {"\x1b[97u", "\x1b[107u", "\x1b[106u", "\x1b[1;1A", "\x1b[1;1B", "\x1b[1;5A", "\x1b[13u"}, {"\x1b[B", "\x1b[13;1:1u"}} {
		for _, b := range bindings[:2] {
			for _, width := range []int{70, 12} {
				probes = append(probes, extensionSelectorProbe{Theme: "dark", Width: width, Title: "Pick", Options: []string{"alpha", "beta", "gamma"}, Bindings: b, Keys: keys, Kitty: true})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/extension_selector.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []showImagesResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps := ActiveTheme(), GetCapabilities()
	previousKitty := IsKittyProtocolActive()
	SetKittyProtocolActive(false)
	restoreKeybindingsAfterTest(t)
	t.Cleanup(func() {
		SetKittyProtocolActive(previousKitty)
		SetCapabilities(previousCaps)
		SetTheme(previousTheme.Name)
	})
	failures := 0
	for i, probe := range probes {
		SetKittyProtocolActive(probe.Kitty)
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var events []string
		selector := NewExtensionSelectorComponent(probe.Title, probe.Options,
			func(option string) { events = append(events, "select:"+option) },
			func() { events = append(events, "cancel") },
			ExtensionSelectorOptions{Description: probe.Description})
		got := showImagesResult{Steps: [][]string{}, Frames: [][]string{selector.Render(probe.Width)}}
		// The host removes the component as soon as a callback runs, so Pi's never sees a key after one; Pig's host polls Done and ignores later keys.
		keys := probe.Keys
		if completed := firstCompletedStep(expected[i].Steps); completed >= 0 {
			keys = keys[:completed+1]
			expected[i].Steps, expected[i].Frames = expected[i].Steps[:completed+1], expected[i].Frames[:completed+2]
		}
		for _, key := range keys {
			selector.HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			if failures++; failures <= 3 {
				t.Errorf("probe %d %+v:\n steps Pig %q Pi %q\n frames Pig %q\n Pi  %q", i, probe, got.Steps, expected[i].Steps, got.Frames, expected[i].Frames)
			}
		}
	}
	t.Logf("%d probes, %d differ", len(probes), failures)
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
