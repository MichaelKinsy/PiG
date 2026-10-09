package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type customComponentProbe struct {
	Kind     string `json:"kind"`
	Content  any    `json:"content,omitempty"`
	Renderer string `json:"renderer"`
	Pad      int    `json:"pad"`
	Pad2     int    `json:"pad2"`
	Width    int    `json:"width"`
}

// custom-message.ts:23-100 and custom-entry.ts:18-69 against pinned Pi: the default message box (label, text blocks joined,
// markdown) and its output pad, a custom renderer's component, a renderer that returns nothing or throws, the entry's
// renderer-failed box and hasContent, across expansion and output pad changes.
func TestCustomMessageAndEntryComponentsMatchPi(t *testing.T) {
	contents := map[string]any{
		"string": "Hello **bold** and `code`\n\n- one\n- two",
		"blocks": []any{map[string]any{"type": "text", "text": "first"}, map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}, map[string]any{"type": "text", "text": "second **b**"}},
		"empty":  "",
		"long":   strings.Repeat("word ", 40),
	}
	var probes []customComponentProbe
	var names []string
	for contentName, content := range contents {
		for _, renderer := range []string{"none", "text", "throw", "undefined"} {
			for _, width := range []int{20, 60} {
				probes = append(probes, customComponentProbe{Kind: "message", Content: content, Renderer: renderer, Pad: 1, Pad2: 0, Width: width},
					customComponentProbe{Kind: "message", Content: content, Renderer: renderer, Pad: 0, Pad2: 2, Width: width})
				names = append(names, "message/"+contentName+"/"+renderer, "message/"+contentName+"/"+renderer+"/pad0")
			}
		}
	}
	for _, renderer := range []string{"text", "throw", "undefined"} {
		for _, width := range []int{20, 60} {
			probes = append(probes, customComponentProbe{Kind: "entry", Renderer: renderer, Pad: 1, Pad2: 0, Width: width},
				customComponentProbe{Kind: "entry", Renderer: renderer, Pad: 0, Pad2: 2, Width: width})
			names = append(names, "entry/"+renderer, "entry/"+renderer+"/pad0")
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/custom_message_components.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Frames     [][]string `json:"frames"`
		HasContent bool       `json:"hasContent"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil))
	failures := 0
	for i, probe := range probes {
		var got [][]string
		hasContent := false
		switch probe.Kind {
		case "message":
			var renderer tui.MessageRenderer
			switch probe.Renderer {
			case "text":
				renderer = func(_ *tui.CustomMessage, options tui.MessageRenderOptions) tui.Component {
					return tui.NewPaddedText(fmt.Sprintf("custom expanded=%t pad=%d", options.Expanded, options.OutputPad), 0, 0, nil)
				}
			case "throw":
				renderer = func(*tui.CustomMessage, tui.MessageRenderOptions) tui.Component { panic("boom") }
			case "undefined":
				renderer = func(*tui.CustomMessage, tui.MessageRenderOptions) tui.Component { return nil }
			}
			c := tui.NewCustomMessageComponent(&tui.CustomMessage{CustomType: "note", Content: probe.Content}, renderer, nil, probe.Pad)
			for _, step := range []struct {
				expanded bool
				pad      int
			}{{false, probe.Pad}, {true, probe.Pad}, {true, probe.Pad2}, {false, probe.Pad2}} {
				c.SetExpanded(step.expanded)
				c.SetOutputPad(step.pad)
				got = append(got, c.Render(probe.Width))
			}
		default:
			renderer := func(_ extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
				switch probe.Renderer {
				case "throw":
					panic(fmt.Errorf("boom"))
				case "undefined":
					return nil
				}
				return tui.NewPaddedText(fmt.Sprintf("custom expanded=%t pad=-", options.Expanded), 0, 0, nil)
			}
			c := NewCustomEntryComponent(CustomEntry{CustomType: "note"}, renderer, probe.Pad)
			for _, step := range []struct {
				expanded bool
				pad      int
			}{{false, probe.Pad}, {true, probe.Pad}, {true, probe.Pad2}} {
				c.SetExpanded(step.expanded)
				c.SetOutputPad(step.pad)
				got = append(got, c.Render(probe.Width))
			}
			hasContent = c.HasContent()
		}
		if !reflect.DeepEqual(got, expected[i].Frames) || (probe.Kind == "entry" && hasContent != expected[i].HasContent) {
			failures++
			if failures <= 6 {
				t.Errorf("%s: %s", names[i], firstBashFrameDifference(got, expected[i].Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
