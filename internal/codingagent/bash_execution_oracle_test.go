package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
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
	// The collapsed "... N more lines (key to expand)" hint names the keys bound to app.tools.expand: rebound, multi-key and unbound.
	for _, bindings := range []map[string][]string{{"app.tools.expand": {"ctrl+e"}}, {"app.tools.expand": {"ctrl+e", "alt+x"}}, {"app.tools.expand": {}}} {
		probes = append(probes, bashProbe{Command: "make test", Pad: 1, Width: 60, Chunks: []string{strings.Repeat("row\n", 30)}, Complete: &bashComplete{Exit: new(0)}, Bindings: bindings})
		names = append(names, fmt.Sprintf("expand hint %v", bindings))
	}
	for i, probe := range randomBashProbes() {
		probes = append(probes, probe)
		names = append(names, fmt.Sprintf("random %d", i))
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bash_execution.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Frames [][]string `json:"frames"`
		Output string     `json:"output"` // truncateTail(getOutput()).content: the output the block draws
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	t.Cleanup(func() { tui.SetAppKeyTextResolver(nil) })
	defer tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	failures := 0
	for i, probe := range probes {
		// The interactive mode's merged tui and app table, which the expand hint reads.
		installCardBindings(t, toolCardProbe{Bindings: probe.Bindings})
		b := tui.NewBashExecutionComponent(probe.Command, nil, probe.Exclude, 1)
		b.SetOutputPad(probe.Pad)
		for _, chunk := range probe.Chunks {
			b.AppendOutput(chunk)
		}
		if c := probe.Complete; c != nil {
			// bash_execution.mjs calls setComplete(exit, cancelled, truncated ? { truncated: true } : undefined, path), which keeps the streamed output.
			var truncation *tui.TruncationResult
			if c.Truncated {
				truncation = &tui.TruncationResult{Truncated: true}
			}
			b.SetComplete(c.Exit, c.Cancelled, truncation, c.Path)
		}
		var got [][]string
		for _, expanded := range []bool{false, true} {
			b.SetExpanded(expanded)
			lines := b.Render(probe.Width)
			got = append(got, lines)
		}
		if !reflect.DeepEqual(got, expected[i].Frames) || b.ContextOutput() != expected[i].Output {
			failures++
			if failures <= 6 {
				t.Errorf("%s: %s; output %q vs Pi %q", names[i], firstBashFrameDifference(got, expected[i].Frames), clipBashOutput(b.ContextOutput()), clipBashOutput(expected[i].Output))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// randomBashProbes draws output chunks that split lines, escape sequences and multi-byte characters anywhere, with CR, backspace, NUL, bell, SGR and cursor sequences, OSC hyperlinks, wide and combining text and very long lines, plus every completion state, output pads from 0 to 3 and widths from 5 to 100.
func randomBashProbes() []bashProbe {
	rng := rand.New(rand.NewSource(20261018))
	pieces := []string{"", "x", "line\n", "alpha beta gamma\n", "日本語のテキスト\n", "e\u0301\n", "😀 ok\n", "\r", "\r\n", "\b", "\x00", "\a", "\x1b[31mred", "\x1b[0m", "\x1b[2K", "\x1b[1;1H", "\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\", "\ttab\t", "\x1b[", "\x1b", "\x1b[38;5;", "208m", strings.Repeat("w", 120), strings.Repeat("日", 70), "\u2028", "\u200b", " \n", "\n\n"}
	commands := []string{"make test", "echo hi", "ls -la | grep x", "echo a\necho b\t$HOME", "日本語", "x" + strings.Repeat("y", 150), "", "\x1b[31mred\x1b[0m"}
	var probes []bashProbe
	for range 400 {
		chunks := make([]string, rng.Intn(14))
		for i := range chunks {
			chunks[i] = pieces[rng.Intn(len(pieces))]
		}
		if rng.Intn(6) == 0 {
			var many strings.Builder
			for i := range 20 + rng.Intn(3000) {
				fmt.Fprintf(&many, "row %d %s\n", i, pieces[rng.Intn(len(pieces))])
			}
			chunks = append(chunks, many.String())
		}
		probe := bashProbe{Command: commands[rng.Intn(len(commands))], Exclude: rng.Intn(5) == 0, Pad: rng.Intn(4), Width: 5 + rng.Intn(96), Chunks: chunks}
		if rng.Intn(4) != 0 {
			c := &bashComplete{Cancelled: rng.Intn(4) == 0, Truncated: rng.Intn(3) == 0}
			if rng.Intn(2) == 0 {
				code := rng.Intn(3)
				c.Exit = &code
			}
			if rng.Intn(2) == 0 {
				c.Path = "/tmp/full.log"
			}
			probe.Complete = c
		}
		probes = append(probes, probe)
	}
	return probes
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
