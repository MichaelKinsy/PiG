package tui

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// highlight.js works on UTF-16 units, so highlighted code can hold a lone surrogate (code with one in it, or a pair split between two scopes). Pi
// wraps and clips the rows with the unit nonprinting and encodes it when the terminal write runs; encoding it as U+FFFD (one cell) in the
// highlighter made a row one cell wider than Pi's and moved its wrap. The rows of a Markdown code block, at widths around the unit, against Pi's.
func TestMarkdownHighlightedCodeWithLoneSurrogatesMatchesPi(t *testing.T) {
	type probe struct {
		Text     string `json:"text"`
		PaddingX int    `json:"paddingX"`
		Width    int    `json:"width"`
	}
	high, low := jsstring.FromUTF16([]uint16{0xd83d}), jsstring.FromUTF16([]uint16{0xde00})
	var probes []probe
	for _, lang := range []string{"js", "python", "go"} {
		for _, unit := range []string{high, low, high + high} {
			for _, width := range []int{12, 20, 40} {
				for fill := width - 8; fill <= width; fill++ {
					for _, code := range []string{
						strings.Repeat("a", fill) + unit + "\nnext",
						"x = '" + strings.Repeat("a", fill) + unit + "'",
						"// " + strings.Repeat("a", fill) + unit + "\nx",
						strings.Repeat("a", fill-1) + unit + "b",
					} {
						probes = append(probes, probe{"```" + lang + "\n" + code + "\n```", 1, width}, probe{"```" + lang + "\n" + code + "\n```", 0, width})
					}
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
