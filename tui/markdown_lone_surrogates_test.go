package tui

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The code of a block quote, a list item and an indented code block keeps a lone surrogate too, against Pi's rows.
func TestMarkdownNestedCodeWithLoneSurrogatesMatchesPi(t *testing.T) {
	type probe struct {
		Text     string `json:"text"`
		PaddingX int    `json:"paddingX"`
		Width    int    `json:"width"`
	}
	high, low := jsstring.FromUTF16([]uint16{0xd83d}), jsstring.FromUTF16([]uint16{0xde00})
	prefix := func(p, text string) string { return p + strings.ReplaceAll(text, "\n", "\n"+p) }
	var probes []probe
	for _, unit := range []string{high, low} {
		for _, width := range []int{12, 20, 40} {
			for fill := width - 10; fill <= width; fill++ {
				code := strings.Repeat("a", fill) + unit + "\nnext"
				fenced := "```js\n" + code + "\n```"
				for _, text := range []string{
					prefix("> ", fenced), "- ```js\n" + prefix("  ", code+"\n```"), prefix("    ", code),
					"para\n\n" + prefix("    ", code), "1. item\n\n" + prefix("       ", code), "~~~\n" + code + "\n~~~",
					"```\n" + code + "\n```", prefix("> ", "text\n\n"+prefix("    ", code)),
				} {
					probes = append(probes, probe{text, 0, width}, probe{text, 1, width})
				}
			}
		}
	}
	input, err := jsstring.MarshalKeepingSurrogates(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/markdown_highlight_surrogate.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := jsstring.UnmarshalKeepingSurrogates(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	failures := 0
	for i, p := range probes {
		got := NewMarkdownWithOptions(p.Text, p.PaddingX, 0, nil, nil, nil).Render(p.Width)
		if strings.Join(got, "\n") != strings.Join(expected[i], "\n") {
			if failures++; failures <= 4 {
				t.Errorf("%q at width %d:\n  Pig %q\n  Pi  %q", p.Text, p.Width, got, expected[i])
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
