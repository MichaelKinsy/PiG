package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// theme.ts getMarkdownTheme(): every span function and highlightCode on plain, nested-SGR, multi-line and empty text
// for both built-in themes, against the pinned Pi (chalk closes and reopens a decoration around its inner close codes).
func TestGetMarkdownThemeMatchesPi(t *testing.T) {
	type probe struct {
		Theme string  `json:"theme"`
		Text  string  `json:"text"`
		Lang  *string `json:"lang,omitempty"`
	}
	texts := []string{"plain", "", "two\nlines", "a\x1b[22mb", "a\x1b[39mb", "a\x1b[23mb\x1b[24mc\x1b[29md", "\x1b[1mbold\x1b[22m tail", "x\r\ny", " lead and trail ", "\nstart", "end\n", "a\n\nb", "tab\tstop", "\x1b[31mred\x1b[0m after", "\x1b[22m", "multi\x1b[22m\nline\x1b[22m"}
	langs := []*string{nil, new("nosuchlanguage"), new("")}
	var probes []probe
	for _, theme := range []string{"dark", "light"} {
		for _, text := range texts {
			for _, lang := range langs {
				probes = append(probes, probe{theme, text, lang})
			}
		}
	}
	input, _ := json.Marshal(probes)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/markdown_theme.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previous) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	failures := 0
	for i, p := range probes {
		SetTheme(p.Theme)
		md := GetMarkdownTheme()
		got := map[string]string{
			"heading": md.Heading(p.Text), "link": md.Link(p.Text), "linkUrl": md.LinkUrl(p.Text), "code": md.Code(p.Text), "codeBlock": md.CodeBlock(p.Text),
			"codeBlockBorder": md.CodeBlockBorder(p.Text), "quote": md.Quote(p.Text), "quoteBorder": md.QuoteBorder(p.Text), "hr": md.Hr(p.Text), "listBullet": md.ListBullet(p.Text),
			"bold": md.Bold(p.Text), "italic": md.Italic(p.Text), "underline": md.Underline(p.Text), "strikethrough": md.Strikethrough(p.Text),
		}
		lang := ""
		if p.Lang != nil {
			lang = *p.Lang
		}
		for field, value := range got {
			if value != expected[i][field] {
				if failures++; failures <= 8 {
					t.Errorf("%s %s(%q): Pig %q, Pi %q", p.Theme, field, p.Text, value, expected[i][field])
				}
			}
		}
		var highlighted []any
		for _, line := range md.HighlightCode(p.Text, lang) {
			highlighted = append(highlighted, line)
		}
		if !reflect.DeepEqual(highlighted, expected[i]["highlight"]) {
			if failures++; failures <= 8 {
				t.Errorf("%s highlightCode(%q, %q): Pig %q, Pi %v", p.Theme, p.Text, lang, highlighted, expected[i]["highlight"])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}
