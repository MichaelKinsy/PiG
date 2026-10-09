package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type borderedLoaderProbe struct {
	Theme       string `json:"theme"`
	Width       int    `json:"width"`
	Message     string `json:"message"`
	Cancellable bool   `json:"cancellable"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type borderedLoaderStep struct {
	Aborts  int  `json:"aborts"`
	Aborted bool `json:"aborted"`
}

type borderedLoaderResult struct {
	Steps  []borderedLoaderStep `json:"steps"`
	Frames [][]string           `json:"frames"`
}

// bordered-loader.ts against pinned Pi: a cancellable loader shows the hint of the configured tui.select.cancel binding and
// aborts (calling onAbort each press) on that key only; a non-cancellable loader has no hint, ignores every key and never aborts.
// Pi source: packages/tui/src/components/cancellable-loader.ts
// mutation-checked: zeroing the results of CancellableLoader.Aborted fails it
func TestBorderedLoaderMatchesPi(t *testing.T) {
	var probes []borderedLoaderProbe
	for _, theme := range []string{"dark", "light"} {
		for _, cancellable := range []bool{true, false} {
			for _, bindings := range []map[string][]string{nil, {KBSelectCancel: {"ctrl+x"}}, {KBSelectCancel: {"escape", "ctrl+g"}}, {KBSelectCancel: {"ctrl+c"}}} {
				for _, keys := range [][]string{{}, {"\x1b"}, {"\x03", "\x03"}, {"\x18", "x", "\x07"}} {
					for _, width := range []int{10, 60} {
						probes = append(probes, borderedLoaderProbe{Theme: theme, Width: width, Message: "Working...", Cancellable: cancellable, Bindings: bindings, Keys: keys})
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bordered_loader.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []borderedLoaderResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		loader := NewBorderedLoader(nil, ActiveTheme(), probe.Message, BorderedLoaderOptions{Cancellable: new(probe.Cancellable)})
		aborts := 0
		loader.OnAbort = func() { aborts++ }
		got := borderedLoaderResult{Steps: []borderedLoaderStep{}, Frames: [][]string{loader.Render(probe.Width)}}
		for _, key := range probe.Keys {
			loader.HandleInput(key)
			got.Steps = append(got.Steps, borderedLoaderStep{Aborts: aborts, Aborted: loader.CancellableContext() != nil && loader.CancellableContext().Aborted()})
			got.Frames = append(got.Frames, loader.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) {
			t.Errorf("probe %d %+v: steps = %+v; Pi = %+v", i, probe, got.Steps, expected[i].Steps)
		}
		if !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			t.Errorf("probe %d %+v:\n%s", i, probe, oracleFrameDifference(got.Frames, expected[i].Frames))
		}
	}
}

// oracleFrameDifference names the first row where two frame sequences differ.
func oracleFrameDifference(got, want [][]string) string {
	for f := range min(len(got), len(want)) {
		for r := range max(len(got[f]), len(want[f])) {
			var g, w string
			if r < len(got[f]) {
				g = got[f][r]
			}
			if r < len(want[f]) {
				w = want[f][r]
			}
			if g != w {
				return fmt.Sprintf("frame %d row %d:\n  Pig %q\n  Pi  %q", f, r, g, w)
			}
		}
	}
	return fmt.Sprintf("frame counts differ: Pig %d, Pi %d", len(got), len(want))
}
