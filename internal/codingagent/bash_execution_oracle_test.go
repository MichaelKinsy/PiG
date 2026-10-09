package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type bashComplete struct {
	Exit      *int   `json:"exit"`
	Cancelled bool   `json:"cancelled"`
	Truncated bool   `json:"truncated"`
	Path      string `json:"path,omitempty"`
}

type bashProbe struct {
	Command  string              `json:"command"`
	Exclude  bool                `json:"exclude"`
	Pad      int                 `json:"pad"`
	Width    int                 `json:"width"`
	Chunks   []string            `json:"chunks"`
	Complete *bashComplete       `json:"complete,omitempty"`
	Bindings map[string][]string `json:"bindings,omitempty"`
}

// bash-execution.ts:35-60,92-215 against pinned Pi: the running header and loader hint, the collapsed and expanded output,
// the hidden-lines hint, the cancelled / exit / truncation status rows and the context truncation of very long output, at several
// widths, output pads and cancel bindings.
func TestBashExecutionComponentMatchesPi(t *testing.T) {
	var many strings.Builder
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&many, "line %d\n", i)
	}
	short := "alpha\nbeta\r\ngamma\rdelta\n"
	fifty := strings.Repeat("x", 60*1024) + "\nlast\n"
	outputs := map[string][]string{
		"none": nil, "short": {short}, "split": {"par", "tial\nnext", " line\n"}, "ansi": {"\x1b[31mred\x1b[0m text\n"},
		"thirty": {strings.Repeat("row\n", 30)}, "wrapped": {strings.Repeat("w", 150) + "\n"},
		"many": {many.String()}, "huge": {fifty},
	}
	completions := map[string]*bashComplete{
		"running": nil, "ok": {Exit: new(0)}, "failed": {Exit: new(7)}, "cancelled": {Cancelled: true},
		"truncated": {Exit: new(0), Truncated: true, Path: "/tmp/full.log"}, "truncated no path": {Exit: new(0), Truncated: true},
		"cancelled truncated": {Cancelled: true, Truncated: true, Path: "/tmp/full.log"}, "failed truncated": {Exit: new(2), Truncated: true, Path: "/tmp/full.log"},
		"no exit": {}, "path without truncation": {Exit: new(0), Path: "/tmp/full.log"},
	}
	var probes []bashProbe
	var names []string
	for outName, chunks := range outputs {
		for compName, complete := range completions {
			probe := bashProbe{Command: "make test", Pad: 1, Width: 60, Chunks: chunks, Complete: complete}
			if outName == "wrapped" || outName == "thirty" {
				probe.Width = 40
			}
			probes = append(probes, probe)
			names = append(names, outName+"/"+compName)
		}
	}
	probes = append(probes,
		bashProbe{Command: "echo hi", Exclude: true, Pad: 1, Width: 60},
		bashProbe{Command: "echo hi", Pad: 0, Width: 60, Chunks: []string{"out\n"}, Complete: &bashComplete{Exit: new(1)}},
		bashProbe{Command: "echo hi", Pad: 1, Width: 60, Bindings: map[string][]string{"tui.select.cancel": {"ctrl+c"}}},
		bashProbe{Command: "echo hi", Pad: 1, Width: 60, Bindings: map[string][]string{"tui.select.cancel": {"escape", "ctrl+g"}}},
		bashProbe{Command: "echo a\necho b\t$HOME", Pad: 1, Width: 20, Chunks: []string{"x\n"}, Complete: &bashComplete{Exit: new(0)}},
	)
	names = append(names, "exclude running", "pad 0", "cancel ctrl+c", "cancel escape+ctrl+g", "multiline command")
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bash_execution.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Frames [][]string `json:"frames"`
		Output string     `json:"output"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	defer tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	failures := 0
	for i, probe := range probes {
		tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), probe.Bindings))
		b := tui.NewBashExecutionComponent(probe.Command, nil, probe.Exclude, 1)
		b.SetOutputPad(probe.Pad)
		for _, chunk := range probe.Chunks {
			b.AppendOutput(chunk)
		}
		if c := probe.Complete; c != nil {
			b.SetCompleteWithOutput(c.Exit, c.Cancelled, c.Truncated, b.GetOutput(), c.Path)
		}
		var got [][]string
		for _, expanded := range []bool{false, true} {
			b.SetExpanded(expanded)
			lines := b.Render(probe.Width)
			got = append(got, lines)
		}
		if !reflect.DeepEqual(got, expected[i].Frames) || b.GetOutput() != expected[i].Output {
			failures++
			if failures <= 6 {
				t.Errorf("%s: %s; output %q vs Pi %q", names[i], firstBashFrameDifference(got, expected[i].Frames), clipBashOutput(b.GetOutput()), clipBashOutput(expected[i].Output))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func firstBashFrameDifference(got, want [][]string) string {
	for frame := range min(len(got), len(want)) {
		for row := range max(len(got[frame]), len(want[frame])) {
			var g, w string
			if row < len(got[frame]) {
				g = got[frame][row]
			}
			if row < len(want[frame]) {
				w = want[frame][row]
			}
			if g != w {
				return fmt.Sprintf("frame %d row %d: Pig %q, Pi %q (%d vs %d rows)", frame, row, g, w, len(got[frame]), len(want[frame]))
			}
		}
	}
	return "frames equal"
}

func clipBashOutput(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}
