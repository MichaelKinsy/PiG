package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type assistantRenderBlock struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type assistantRenderMessage struct {
	Blocks       []assistantRenderBlock `json:"blocks"`
	StopReason   string                 `json:"stopReason"`
	ErrorMessage string                 `json:"errorMessage,omitempty"`
}

type assistantRenderOp struct {
	Kind      string                  `json:"kind"`
	N         int                     `json:"n"`
	Text      string                  `json:"text,omitempty"`
	Flag      bool                    `json:"flag,omitempty"`
	Message   *assistantRenderMessage `json:"message,omitempty"`
	Streaming bool                    `json:"streaming,omitempty"`
}

type assistantRenderProbe struct {
	Theme   string                 `json:"theme"`
	Message assistantRenderMessage `json:"message"`
	Hide    bool                   `json:"hide"`
	Label   *string                `json:"label"`
	Pad     int                    `json:"pad"`
	Ops     []assistantRenderOp    `json:"ops"`
	Widths  []int                  `json:"widths"`
}

func (m assistantRenderMessage) toTUI() AssistantMessage {
	out := AssistantMessage{StopReason: m.StopReason, ErrorMessage: m.ErrorMessage}
	for _, b := range m.Blocks {
		switch b.Kind {
		case "tool":
			out.Content = append(out.Content, AssistantContentBlock{Type: "toolCall"})
		case "thinking":
			out.Content = append(out.Content, AssistantContentBlock{Type: "thinking", Thinking: b.Text})
		default:
			out.Content = append(out.Content, AssistantContentBlock{Type: "text", Text: b.Text})
		}
	}
	return out
}

// AssistantMessageComponent.render and updateContent (components/assistant-message.ts) against pinned Pi, byte for byte: text and thinking runs (visible and
// collapsed to the label), runs split by tool calls, whitespace-only blocks, the length/aborted/error diagnostics and their suppression when tool calls are
// present, the OSC 133 zone marks only without tool calls, output padding, and updates through setOutputPad, setHiddenThinkingLabel, setHideThinkingBlock
// and updateContent.
func TestAssistantMessageComponentRenderMatchesPi(t *testing.T) {
	text := func(s string) assistantRenderBlock { return assistantRenderBlock{Kind: "text", Text: s} }
	think := func(s string) assistantRenderBlock { return assistantRenderBlock{Kind: "thinking", Text: s} }
	tool := assistantRenderBlock{Kind: "tool"}
	str := func(s string) *string { return &s }
	messages := []assistantRenderMessage{
		{StopReason: "stop"},
		{Blocks: []assistantRenderBlock{text("hello world")}, StopReason: "stop"},
		{Blocks: []assistantRenderBlock{text("  \n ")}, StopReason: "stop"},
		{Blocks: []assistantRenderBlock{think("pondering the question"), text("the answer is **42**")}, StopReason: "stop"},
		{Blocks: []assistantRenderBlock{think("first"), think("second"), text("done")}, StopReason: "stop"},
		{Blocks: []assistantRenderBlock{think("a"), tool, think("b"), text("c")}, StopReason: "toolUse"},
		{Blocks: []assistantRenderBlock{text("calling"), tool}, StopReason: "toolUse"},
		{Blocks: []assistantRenderBlock{tool}, StopReason: "toolUse"},
		{Blocks: []assistantRenderBlock{think("   "), text("only text")}, StopReason: "stop"},
		{Blocks: []assistantRenderBlock{text("partial output that was cut")}, StopReason: "length"},
		{Blocks: []assistantRenderBlock{text("partial"), tool}, StopReason: "length"},
		{Blocks: []assistantRenderBlock{text("partial")}, StopReason: "aborted"},
		{Blocks: []assistantRenderBlock{text("partial")}, StopReason: "aborted", ErrorMessage: "Request was aborted"},
		{Blocks: []assistantRenderBlock{text("partial")}, StopReason: "aborted", ErrorMessage: "user pressed escape"},
		{Blocks: []assistantRenderBlock{tool}, StopReason: "aborted", ErrorMessage: "ignored with tool calls"},
		{StopReason: "error"},
		{Blocks: []assistantRenderBlock{text("before the failure")}, StopReason: "error", ErrorMessage: "429 rate limited, retry in 20s"},
		{Blocks: []assistantRenderBlock{tool}, StopReason: "error", ErrorMessage: "hidden by tool call"},
		{Blocks: []assistantRenderBlock{think("thought"), text("# Heading\n\n- item\n- item two\n\n```go\nx := 1\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |")}, StopReason: "stop"},
		{Blocks: []assistantRenderBlock{text("日本語のメッセージ 🙂 wide cells and a very long line that has to wrap around in narrow widths")}, StopReason: "stop"},
	}
	ops := [][]assistantRenderOp{
		nil,
		{{Kind: "pad", N: 0}, {Kind: "pad", N: 2}},
		{{Kind: "hide", Flag: true}, {Kind: "label", Text: "Hidden..."}, {Kind: "hide", Flag: false}},
		{{Kind: "update", Message: &messages[3], Streaming: true}, {Kind: "update", Message: &messages[16]}},
	}
	var probes []assistantRenderProbe
	for _, theme := range []string{"dark", "light"} {
		for _, m := range messages {
			for _, hide := range []bool{false, true} {
				for _, label := range []*string{nil, str("Pondering...")} {
					for _, pad := range []int{0, 1} {
						for _, o := range ops {
							probes = append(probes, assistantRenderProbe{Theme: theme, Message: m, Hide: hide, Label: label, Pad: pad, Ops: o, Widths: []int{1, 12, 40, 80}})
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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/assistant_message_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][][][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		message := probe.Message.toTUI()
		label := ""
		if probe.Label != nil {
			label = *probe.Label
		}
		pad := probe.Pad
		component := NewAssistantMessageComponent(&message, probe.Hide, nil, label, &pad, nil)
		check := func(frame int) {
			for w, width := range probe.Widths {
				got := component.Render(width)
				if !reflect.DeepEqual(nonNil(got), nonNil(expected[i][frame][w])) {
					if failures++; failures <= 4 {
						spec, _ := json.Marshal(probe)
						t.Errorf("frame %d width %d %s\n  Pig %q\n  Pi  %q", frame, width, spec, got, expected[i][frame][w])
					}
				}
			}
		}
		check(0)
		for o, op := range probe.Ops {
			switch op.Kind {
			case "pad":
				component.SetOutputPad(op.N)
			case "label":
				component.SetHiddenThinkingLabel(op.Text)
			case "hide":
				component.SetHideThinkingBlock(op.Flag)
			case "update":
				component.UpdateContent(op.Message.toTUI(), op.Streaming)
			}
			check(o + 1)
		}
	}
	if failures > 4 {
		t.Errorf("%d renders differ from Pi", failures)
	}
}
