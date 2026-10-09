package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type assistantLabelBlock struct {
	Thinking bool   `json:"thinking"`
	Text     string `json:"text"`
}

type assistantLabelOp struct {
	Kind  string `json:"kind"`
	Value any    `json:"value"`
}

type assistantLabelProbe struct {
	Theme   string                `json:"theme"`
	Width   int                   `json:"width"`
	Hide    bool                  `json:"hide"`
	Label   *string               `json:"label"`
	Content []assistantLabelBlock `json:"content"`
	Ops     []assistantLabelOp    `json:"ops"`
}

var assistantLabelEscapes = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[A-Za-z]|\\][^\x07]*\x07)")

// assistant-message.ts setHiddenThinkingLabel and setHideThinkingBlock against pinned Pi: a collapsed thinking run shows the
// current label (the empty string is a label), a visible one shows the thinking text, and each setter redraws the content.
func TestAssistantMessageHiddenThinkingLabelMatchesPi(t *testing.T) {
	contents := [][]assistantLabelBlock{
		{{Thinking: true, Text: "deep thoughts"}, {Text: "the answer"}},
		{{Text: "first"}, {Thinking: true, Text: "mid thought"}, {Text: "last"}},
		{{Thinking: true, Text: "one"}, {Thinking: true, Text: "two"}, {Text: "end"}},
		{{Text: "no thinking at all"}},
	}
	ptr := func(s string) *string { return &s }
	labels := []*string{nil, ptr("Pondering"), ptr(""), ptr("🧠 a very long collapsed thinking label that wraps around")}
	scripts := [][]assistantLabelOp{
		{},
		{{"label", "Changed"}},
		{{"label", ""}, {"hide", false}, {"hide", true}},
		{{"hide", false}, {"label", "Hidden now"}, {"hide", true}},
		{{"label", "A"}, {"label", "B"}, {"label", "Thinking..."}},
	}
	var probes []assistantLabelProbe
	for _, theme := range []string{"dark", "light"} {
		for _, content := range contents {
			for _, label := range labels {
				for _, hide := range []bool{true, false} {
					for _, ops := range scripts {
						for _, width := range []int{60, 20} {
							probes = append(probes, assistantLabelProbe{Theme: theme, Width: width, Hide: hide, Label: label, Content: content, Ops: ops})
						}
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/assistant_message_label.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Frames [][]string `json:"frames"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	text := func(lines []string) []string {
		var out []string
		for _, line := range lines {
			out = append(out, strings.TrimRight(assistantLabelEscapes.ReplaceAllString(line, ""), " "))
		}
		for len(out) > 0 && out[len(out)-1] == "" {
			out = out[:len(out)-1]
		}
		return out
	}
	previousCaps := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previousCaps) })
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		component := NewAssistantMessageComponent(nil, probe.Hide, nil, "", nil, nil)
		if probe.Label != nil {
			component.SetHiddenThinkingLabel(*probe.Label)
		}
		segments := make([]AssistantSegment, len(probe.Content))
		for j, block := range probe.Content {
			segments[j] = AssistantSegment(block)
		}
		component.SetContent(segments)
		got := [][]string{text(component.Render(probe.Width))}
		for _, op := range probe.Ops {
			switch op.Kind {
			case "label":
				component.SetHiddenThinkingLabel(op.Value.(string))
			case "hide":
				component.SetHideThinkingBlock(op.Value.(bool))
			}
			got = append(got, text(component.Render(probe.Width)))
		}
		var want [][]string
		for _, frame := range expected[i].Frames {
			want = append(want, text(frame))
		}
		if !reflect.DeepEqual(got, want) {
			failures++
			if failures <= 4 {
				t.Errorf("probe %d %+v:\nPig = %q\nPi  = %q", i, probe, got, want)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
