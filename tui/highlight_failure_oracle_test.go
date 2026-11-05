//go:build !pig_strip_syntax_highlight

package tui

import (
	"bytes"
	"encoding/json"
	"maps"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// theme.ts highlightCode (Pi 0f21a331) returns the code in the mdCodeBlock color when the highlight throws, and getMarkdownTheme().highlightCode is that function, so a
// Markdown code block whose highlight fails is drawn in that color. The theme has no color for the syntaxKeyword token, so highlighting a keyword calls theme.fg, which
// throws `Unknown theme color` in Pi and panics with it here. Code with no keyword highlights as usual. The expected lines are the installed Pi's.
func TestHighlightFailureMatchesPi(t *testing.T) {
	type probe struct {
		Text string `json:"text"`
		Code string `json:"code"`
		Lang string `json:"lang"`
	}
	fence := func(lang, code string) probe { return probe{"```" + lang + "\n" + code + "\n```", code, lang} }
	probes := []probe{
		fence("javascript", "const answer = 42;\nreturn answer"),
		fence("python", "def f(x):\n    return x * 2"),
		fence("json", `{"a": 1}`),
		// highlight.js names no language "not-a-language" in any registry state; "text" is its plaintext alias, which LoadAllHighlightLanguages registers, so a "text" probe depends on which test ran before.
		fence("not-a-language", "no highlighter for this language"),
		fence("", "no language"),
	}
	input, _ := json.Marshal(probes)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/highlight_failure.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		Rendered  []string `json:"rendered"`
		Highlight []string `json:"highlight"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	withTrueColor(t, true)
	SetTheme("dark")
	dark := ActiveTheme()
	broken, err := NewThemeFromANSIPalette(dark.Name, maps.Clone(dark.fgAnsi), maps.Clone(dark.bgAnsi), dark.GetColorMode())
	if err != nil {
		t.Fatal(err)
	}
	delete(broken.fgAnsi, "syntaxKeyword")
	previous := activeTheme.Load()
	t.Cleanup(func() { activeTheme.Store(previous) })
	activeTheme.Store(broken)
	for i, p := range probes {
		theme := GetMarkdownTheme()
		if got := theme.HighlightCode(p.Code, p.Lang); !reflect.DeepEqual(got, expected[i].Highlight) {
			t.Errorf("highlightCode(%q, %q): Pig %q, Pi %q", p.Code, p.Lang, got, expected[i].Highlight)
		}
		if got := NewMarkdownWithOptions(p.Text, 0, 0, &theme, nil, nil).Render(40); !reflect.DeepEqual(got, expected[i].Rendered) {
			t.Errorf("Markdown %q: Pig %q, Pi %q", p.Text, got, expected[i].Rendered)
		}
	}
}
