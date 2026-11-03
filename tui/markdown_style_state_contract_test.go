package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Pi-oracle comparison of how markdown.ts applies the default text style when the style's color function has state: it numbers its calls, so the rows record
// how often the style is applied (text segments, the sentinel that finds the style prefix) and the prefix is computed once per component until invalidate.

type markdownStateProbe struct {
	Text          string               `json:"text"`
	PaddingX      int                  `json:"paddingX"`
	Color         bool                 `json:"color"`
	Bold          bool                 `json:"bold"`
	Italic        bool                 `json:"italic"`
	Strikethrough bool                 `json:"strikethrough"`
	Underline     bool                 `json:"underline"`
	Ops           []markdownContractOp `json:"ops"`
}

type markdownStateRender struct {
	Rows  []string `json:"rows"`
	Calls int      `json:"calls"`
}

func piMarkdownState(t *testing.T, probes []markdownStateProbe) [][]markdownStateRender {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/markdown_style_state_contract.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]markdownStateRender
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(expected), len(probes))
	}
	return expected
}

func (p markdownStateProbe) run() []markdownStateRender {
	taggedTheme := taggedMarkdownTheme()
	calls := 0
	style := &DefaultTextStyle{Bold: p.Bold, Italic: p.Italic, Strikethrough: p.Strikethrough, Underline: p.Underline}
	if p.Color {
		style.Color = func(text string) string {
			calls++
			return "<dc" + strconv.Itoa(calls) + ">" + text + "</dc>"
		}
	}
	md := NewMarkdownWithOptions(p.Text, p.PaddingX, 0, &taggedTheme, style, nil)
	var renders []markdownStateRender
	for _, op := range p.Ops {
		switch op.Op {
		case "render":
			renders = append(renders, markdownStateRender{Rows: nonNil(md.Render(op.Width)), Calls: calls})
		case "setText":
			md.SetText(op.Text)
		case "invalidate":
			md.Invalidate()
		}
	}
	return renders
}

// markdownStateDocs are documents whose blocks apply the default style in every container that passes it on: paragraphs, headings, quotes, lists, code and nested containers (tables and task items are left out: markdown.ts renders each cell's tokens twice, to measure and to wrap, and not at all when the table falls back to raw text; a task item renders its text once more in PiG to find the marker, so a color function that counts its calls sees other counts than PiG makes).
func markdownStateDocs() []string {
	return []string{
		"plain", "a **b** c", "a\n\nb", "# h\n\ntext", "> q **b**\n> more", "- a\n- b **c**", "1. a\n2. b", "- a\n  - b\n    - c", "> - a\n>   > b",
		"```\ncode\n```", "text\n\n```\ncode\n```\n\nafter", "a  \nb", "> a\n\n> b", "- a\n\n- b", "### h **b**\n\n> q\n\n- l\n\ntext",
		"a\n\n\n\nb", "> # h\n> - l", "- > q\n  > r", "***\n\ntext", "<div>x</div>\n\ntext", "\\*e\\*", "[l](http://u.v) `c`", "$x$ text",
	}
}

// TestMarkdownDefaultStyleCallsMatchPi compares rows and the number of color calls of a first render, for every combination of text decorations, at three widths.
// several widths, with invalidate and setText between them.
func TestMarkdownDefaultStyleCallsMatchPi(t *testing.T) {
	var probes []markdownStateProbe
	for i, doc := range markdownStateDocs() {
		for variant := range 8 {
			probe := markdownStateProbe{Text: doc, PaddingX: variant % 2, Color: variant != 7, Bold: variant == 1 || variant == 7, Italic: variant == 2 || variant >= 4, Strikethrough: variant == 3, Underline: variant == 5}
			probe.Ops = markdownContractRenderOps([]int{30, 10, 6}[(i+variant)%3])
			probes = append(probes, probe)
		}
	}
	expected := piMarkdownState(t, probes)
	failures := 0
	for i, probe := range probes {
		got := probe.run()
		if !reflect.DeepEqual(got, expected[i]) {
			failures++
			if failures <= 8 {
				t.Errorf("%s: %q\nPig %s\nPi  %s", fmt.Sprintf("probe %d", i), probe.Text, mustJSON(got), mustJSON(expected[i]))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
