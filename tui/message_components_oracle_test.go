package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type messageComponentProbe struct {
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`
	Tokens  int    `json:"tokens,omitempty"`
	Pad     int    `json:"pad"`
	Width   int    `json:"width"`
	Current bool   `json:"current,omitempty"`
}

// compaction-summary-message.ts, branch-summary-message.ts, skill-invocation-message.ts, show-images-selector.ts and theme-selector.ts against pinned
// Pi: collapsed and expanded rendering at several widths and output pads, including a token count with thousands separators.
func TestSummaryAndSkillMessageComponentsMatchPi(t *testing.T) {
	texts := []string{"short summary", "# Heading\n\nA **bold** paragraph with `code` and a long line that has to wrap across several rows of the box.\n\n- one\n- two", "line one\nline two\n\nline four"}
	var probes []messageComponentProbe
	for _, width := range []int{20, 60} {
		for _, pad := range []int{0, 1} {
			for _, text := range texts {
				for _, tokens := range []int{5, 1234, 1234567} {
					probes = append(probes, messageComponentProbe{Kind: "compaction", Text: text, Tokens: tokens, Pad: pad, Width: width})
				}
				probes = append(probes, messageComponentProbe{Kind: "branch", Text: text, Pad: pad, Width: width},
					messageComponentProbe{Kind: "skill", Name: "review-skill", Text: text, Pad: pad, Width: width})
			}
		}
		for _, current := range []bool{true, false} {
			probes = append(probes, messageComponentProbe{Kind: "images", Current: current, Width: width})
		}
		for _, name := range ActiveThemeRegistry().Names() {
			probes = append(probes, messageComponentProbe{Kind: "theme", Name: name, Width: width})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/message_components.mjs", pigversion.UpstreamVersion)
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
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() { SetCapabilities(previousCaps); storeActiveTheme(previousTheme) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	restoreKeybindingsAfterTest(t)
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	// Rows are compared byte for byte, colours and OSC 8 links included.
	plain := slices.Clone[[]string]
	failures := 0
	for i, probe := range probes {
		var got [][]string
		switch probe.Kind {
		case "compaction":
			c := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: probe.Text, TokensBefore: probe.Tokens}, nil, probe.Pad)
			got = append(got, plain(c.Render(probe.Width)))
			c.SetExpanded(true)
			got = append(got, plain(c.Render(probe.Width)))
		case "branch":
			c := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: probe.Text}, nil, probe.Pad)
			got = append(got, plain(c.Render(probe.Width)))
			c.SetExpanded(true)
			got = append(got, plain(c.Render(probe.Width)))
		case "skill":
			c := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: probe.Name, Content: probe.Text}, nil, probe.Pad)
			got = append(got, plain(c.Render(probe.Width)))
			c.SetExpanded(true)
			got = append(got, plain(c.Render(probe.Width)))
		case "theme":
			got = append(got, plain(NewThemeSelectorComponent(probe.Name, nil, nil, nil).Render(probe.Width)))
		default:
			got = append(got, plain(NewShowImagesSelectorComponent(probe.Current, nil, nil).Render(probe.Width)))
		}
		if !reflect.DeepEqual(got, expected[i].Frames) {
			failures++
			if failures <= 4 {
				t.Errorf("probe %d %+v:\n  Pig %q\n  Pi  %q", i, probe, got, expected[i].Frames)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
