package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type showImagesProbe struct {
	Theme    string              `json:"theme"`
	Width    int                 `json:"width"`
	Current  bool                `json:"current"`
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type showImagesResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// show-images-selector.ts (a bordered SelectList of Yes/No with a 12..32 primary column) against pinned Pi: every frame and the select and
// cancel callbacks after each key, with default and rebound select keys.
func TestShowImagesSelectorMatchesPi(t *testing.T) {
	scripts := [][]string{
		nil,
		{"\x1b[A"},
		{"\x1b[B"},
		{"\x1b[B", "\x1b[B", "\x1b[B"},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\r"},
		{"\x1b[B", "\r"},
		{"\n"},
		{"\x1b"},
		{"\x03"},
		{"y"},
		{"\x1b[5~", "\x1b[6~", "\x1b[H", "\x1b[F", "\r"},
		{"\x1b[B", "x", "\r", "\x1b"},
		{"\x13"},
		{"\x1b[B", "\x13"},
		{"\x18"},
		{"j", "j"},
		{"\x0e", "k", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.confirm": {"ctrl+s"}},
		{"tui.select.cancel": {"ctrl+x"}},
		{"tui.select.down": {"ctrl+n", "j"}, "tui.select.up": {"k"}},
	}
	var probes []showImagesProbe
	for _, theme := range []string{"dark", "light"} {
		for _, width := range []int{70, 24, 8} {
			for _, current := range []bool{true, false} {
				for _, b := range bindings {
					for _, keys := range scripts {
						probes = append(probes, showImagesProbe{Theme: theme, Width: width, Current: current, Bindings: b, Keys: keys})
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/show_images_selector.mjs", pigversion.UpstreamVersion)
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
	// Pi runs without the Kitty keyboard protocol, so LF is Enter; an earlier test may have left it active.
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
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var events []string
		selector := NewShowImagesSelectorComponent(probe.Current,
			func(show bool) { events = append(events, "select:"+strconv.FormatBool(show)) },
			func() { events = append(events, "cancel") })
		got := showImagesResult{Steps: [][]string{}, Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			selector.GetSelectList().HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			if failures++; failures <= 3 {
				t.Errorf("probe %d %+v:\n steps Pig %q Pi %q\n frames Pig %q\n Pi  %q", i, probe, got.Steps, expected[i].Steps, got.Frames, expected[i].Frames)
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
