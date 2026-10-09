package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type bashExecutionComplete struct {
	ExitCode       *int   `json:"exitCode"`
	Cancelled      bool   `json:"cancelled"`
	Truncated      bool   `json:"truncated"`
	FullOutputPath string `json:"fullOutputPath"`
}

type bashExecutionProbe struct {
	Theme              string `json:"theme"`
	Width              int    `json:"width"`
	Command            string `json:"command"`
	ExcludeFromContext bool   `json:"excludeFromContext"`
	OutputPad          int    `json:"outputPad"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string    `json:"bindings,omitempty"`
	Chunks   []string               `json:"chunks"`
	Expanded bool                   `json:"expanded"`
	Complete *bashExecutionComplete `json:"complete,omitempty"`
}

// bash-execution.ts against pinned Pi: the rows of a `!` or `!!` block, running (with the loader's
// tui.select.cancel hint) and completed (exit status, cancellation, truncation path), collapsed
// and expanded, at both outputPad values, byte for byte: Text pads every row to the width, the
// header is theme.fg(colorKey, theme.bold(...)), the output passes the bash tool's context limits, and the block ends
// at its bottom border.
func TestBashExecutionComponentMatchesPi(t *testing.T) {
	exit := func(code int) *int { return &code }
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprint(i + 1)
	}
	states := []struct {
		chunks   []string
		expanded bool
		complete *bashExecutionComplete
	}{
		{chunks: nil},
		{chunks: []string{"waiting"}},
		{chunks: []string{"hello world\n"}, complete: &bashExecutionComplete{ExitCode: exit(0)}},
		{chunks: []string{"a.txt\r\n", "b.txt"}, complete: &bashExecutionComplete{ExitCode: exit(2)}},
		{chunks: []string{"\x1b[31mred\x1b[0m\rover"}, complete: &bashExecutionComplete{Cancelled: true}},
		{chunks: []string{strings.Join(many, "\n")}, complete: &bashExecutionComplete{ExitCode: exit(0)}},
		{chunks: []string{strings.Join(many, "\n")}, expanded: true, complete: &bashExecutionComplete{ExitCode: exit(1), Truncated: true, FullOutputPath: "/tmp/full.log"}},
		{chunks: nil, complete: &bashExecutionComplete{}},
	}
	var probes []bashExecutionProbe
	for _, theme := range []string{"dark", "light"} {
		for _, command := range []string{"ls -la", "", "echo value value value value value value value value", "printf first\nprintf second"} {
			for _, exclude := range []bool{false, true} {
				for _, state := range states {
					for _, width := range []int{12, 40} {
						for _, pad := range []int{0, 1} {
							probes = append(probes, bashExecutionProbe{Theme: theme, Width: width, Command: command, ExcludeFromContext: exclude, OutputPad: pad, Chunks: state.chunks, Expanded: state.expanded, Complete: state.complete})
						}
					}
				}
			}
		}
	}
	probes = append(probes, bashExecutionProbe{Theme: "dark", Width: 60, Command: "sleep 9", OutputPad: 1, Bindings: map[string][]string{KBSelectCancel: {"ctrl+g"}}})
	// truncateTail's context limits (2000 lines, 50 KiB) bound the lines the block shows and counts as hidden. Hitting them also
	// shows the full-output path when the result itself was not truncated (bash-execution.ts:200 wasTruncated).
	huge := make([]string, 2100)
	for i := range huge {
		huge[i] = fmt.Sprint(i + 1)
	}
	wide := strings.Repeat(strings.Repeat("x", 99)+"\n", 600)
	for _, chunks := range [][]string{{strings.Join(huge, "\n")}, {wide}, {strings.Repeat("界", 20000)}} {
		for _, expanded := range []bool{false, true} {
			probes = append(probes, bashExecutionProbe{Theme: "dark", Width: 40, Command: "seq", OutputPad: 1, Chunks: chunks, Expanded: expanded, Complete: &bashExecutionComplete{ExitCode: exit(0)}})
		}
		probes = append(probes, bashExecutionProbe{Theme: "dark", Width: 60, Command: "seq", OutputPad: 1, Chunks: chunks, Complete: &bashExecutionComplete{ExitCode: exit(0), FullOutputPath: "/tmp/full.log"}})
	}
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
	var expected [][]string
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
		block := NewBashExecutionComponent(probe.Command, nil, probe.ExcludeFromContext, probe.OutputPad)
		for _, chunk := range probe.Chunks {
			block.AppendOutput(chunk)
		}
		if probe.Expanded {
			block.SetExpanded(true)
		}
		if c := probe.Complete; c != nil {
			block.SetCompleteWithOutput(c.ExitCode, c.Cancelled, c.Truncated, block.GetOutput(), c.FullOutputPath)
		}
		if got := block.Render(probe.Width); !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("probe %d %+v:\n got %q\nwant %q", i, probe, got, expected[i])
		}
	}
}
