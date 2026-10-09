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

type editorStatusProbe struct {
	Theme    string   `json:"theme"`
	Level    string   `json:"level"`
	Message  string   `json:"message"`
	Frames   []string `json:"frames"`
	Ticks    int      `json:"ticks"`
	Rows     int      `json:"rows"`
	Ups      int      `json:"ups"`
	PaddingX int      `json:"paddingX"`
	Width    int      `json:"width"`
}

// CustomEditor.renderTopBorder (custom-editor.ts) with an embedded WorkingStatusIndicator against pinned Pi: the status in the top border
// (message, spinner frame, custom frames, the thinking-level border colour), the centred overflow label of a scrolled paste when it fits, the
// spinner alone when it does not, and the plain border when the status is empty or the width is too small.
func TestEditorStatusBorderMatchesPi(t *testing.T) {
	probes := editorStatusProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_status_border.mjs", pigversion.UpstreamVersion)
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
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	failures, withStatus, withOverflow := 0, 0, 0
	for i, probe := range probes {
		got := renderEditorStatusProbe(probe)
		if strings.Contains(got[0], "more") {
			withOverflow++
		}
		if strings.Contains(got[0], probe.Message) && probe.Message != "" {
			withStatus++
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 5 {
				t.Errorf("%+v:\n  Pig %q\n  Pi  %q", probe, got, expected[i])
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if withStatus < len(probes)/20 || withOverflow < len(probes)/20 {
		t.Errorf("corpus too thin: %d with the status text, %d with the overflow label of %d probes", withStatus, withOverflow, len(probes))
	}
}

func editorStatusProbes() []editorStatusProbe {
	var probes []editorStatusProbe
	frames := [][]string{nil, {"x"}, {"⠋", "⠙", "⠹"}, {"🚀", "🌕"}}
	for _, theme := range []string{"dark", "light"} {
		for _, level := range []string{"off", "high"} {
			for _, message := range []string{"Working", "", "A considerably longer working message than most"} {
				for _, fr := range frames {
					for _, ticks := range []int{0, 1, 2} {
						for _, shape := range [][2]int{{0, 0}, {6, 0}, {30, 4}} {
							for _, width := range []int{2, 4, 8, 12, 20, 30, 40, 80} {
								probes = append(probes, editorStatusProbe{Theme: theme, Level: level, Message: message, Frames: fr, Ticks: ticks, Rows: shape[0], Ups: shape[1], PaddingX: ticks, Width: width})
							}
						}
					}
				}
			}
		}
	}
	// Every width around the points where the overflow label and the status stop fitting.
	for _, message := range []string{"Working", "Hi", "A considerably longer working message than most"} {
		for _, fr := range [][]string{nil, {"🚀", "🌕"}} {
			for _, shape := range [][2]int{{0, 0}, {30, 0}, {30, 4}} {
				for width := 2; width <= 64; width++ {
					probes = append(probes, editorStatusProbe{Theme: "dark", Level: "high", Message: message, Frames: fr, Rows: shape[0], Ups: shape[1], Width: width})
				}
			}
		}
	}
	return probes
}

// renderEditorStatusProbe renders one probe's editor with Pig, in the probe's theme.
func renderEditorStatusProbe(probe editorStatusProbe) []string {
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme(probe.Theme)
	e := NewEditor()
	e.EmbedWorkingStatus = true
	e.ThinkingLevel = probe.Level
	e.SetFocused(true)
	e.SetPaddingX(probe.PaddingX)
	for j := range probe.Rows {
		if j > 0 {
			e.HandleInput("\n")
		}
		for _, ch := range fmt.Sprintf("row %d", j) {
			e.HandleInput(string(ch))
		}
	}
	for range probe.Ups {
		e.HandleInput("\x1b[A")
	}
	var options *LoaderIndicatorOptions
	if probe.Frames != nil {
		options = &LoaderIndicatorOptions{Frames: probe.Frames}
	}
	indicator := &StatusIndicator{Kind: "working", Loader: NewLoader(nil, ThemeFg("accent"), ThemeFg("muted"), probe.Message, options)}
	for range probe.Ticks {
		indicator.Tick()
	}
	e.SetWorkingStatusIndicator(indicator)
	return e.Render(probe.Width)
}

// TestEditorStatusBorderProbeDump prints the corpus for the Pi side of the editor-status-border parity scenario.
func TestEditorStatusBorderProbeDump(t *testing.T) {
	line, err := json.Marshal(editorStatusProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("editorstatus-probes:%s\n", line)
}

// TestEditorStatusBorderParity prints Pig's renders of the corpus, one JSON line per probe, for the editor-status-border parity scenario.
func TestEditorStatusBorderParity(t *testing.T) {
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for _, probe := range editorStatusProbes() {
		line, err := json.Marshal(renderEditorStatusProbe(probe))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("editorstatus-observation:%s\n", line)
	}
}
