//go:build !pig_strip_syntax_highlight

package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type highlightHTMLProbe struct {
	HTML  string   `json:"html"`
	Theme []string `json:"theme"`
}

type highlightHTMLResult struct {
	Out   *string `json:"out,omitempty"`
	Error string  `json:"error,omitempty"`
}

// highlightHTMLProbes builds seeded highlight.js-shaped HTML: nested and unbalanced spans, every attribute spelling the scope reader sees, scopes with dots
// and dashes, character references (named, decimal, hex, malformed, too long), newlines inside scopes, and stray markup, against themes that map some scopes.
func highlightHTMLProbes() []highlightHTMLProbe {
	rng := rand.New(rand.NewPCG(7, 11))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	opens := []string{
		`<span class="hljs-keyword">`, `<span class="hljs-title function_">`, `<span class="hljs-title.function">`, `<span class="hljs-meta-keyword">`, `<span class='hljs-string'>`,
		`<span class="x hljs-comment y">`, `<span class="hljs-">`, `<span class="">`, `<span>`, `<span\tclass="hljs-number">`, `<span\nclass = "hljs-string">`, `<span class=hljs-keyword>`,
		`<span id="a" class="hljs-keyword">`, `<spanx class="hljs-keyword">`, `<span`, `<span class="hljs-keyword"`, `<span class="hljs-title class_">`, `<span class="language-go hljs-built_in">`,
		`<SPAN class="hljs-keyword">`, `<span class="hljs-attr" data-x=">">`, `<span  class="hljs-string"  >`, `<span\rclass="hljs-comment">`,
	}
	texts := []string{
		"x", " ", "func main() {", "a\nb", "\n", "\n\n", "line one\nline two\n", "&amp;", "&lt;&gt;", "&quot;&#39;", "&#x41;&#65;", "&nbsp;", "&unknown;", "&", "& ;", "&#;", "&#x;", "&#xD800;", "&#1114112;",
		"&#128512;", "&" + strings.Repeat("a", 20) + ";", "&aa;" + "&", "<", "</", "</span", "</spanx>", "日本語", "😀", "\x00", "\t", "if (a < b && c > d)", "&amp", "a&b;c",
	}
	closes := []string{`</span>`, `</span>`, `</span>`, `</span >`, `</SPAN>`}
	keys := []string{"default", "keyword", "title", "title.function", "string", "comment", "meta", "meta-keyword", "number", "built_in", "attr", "x"}
	var probes []highlightHTMLProbe
	for range 4000 {
		var html strings.Builder
		for range 1 + rng.IntN(10) {
			switch rng.IntN(4) {
			case 0:
				html.WriteString(pick(opens))
			case 1:
				html.WriteString(pick(closes))
			default:
				html.WriteString(pick(texts))
			}
		}
		var theme []string
		for _, key := range keys {
			if rng.IntN(3) == 0 {
				theme = append(theme, key)
			}
		}
		probes = append(probes, highlightHTMLProbe{HTML: html.String(), Theme: theme})
	}
	// Class attribute spellings: the whitespace JavaScript's \s accepts before, inside and after the attribute, quoting, and which hljs- class wins.
	spaces := []string{" ", "\t", "\n", "\r", "\f", "\v", "\u00a0", "\u2028", "\u2029", "\ufeff", "\u1680", "\u2003", "\u3000", "\u180e", "\u200b", "\u0085", ""}
	for _, space := range spaces {
		for _, tag := range []string{
			"<span" + space + "class=\"hljs-keyword\">", "<span class" + space + "=\"hljs-keyword\">", "<span class=" + space + "\"hljs-keyword\">", "<span class=\"hljs-keyword" + space + "hljs-string\">",
			"<span class=\"" + space + "hljs-keyword\">", "<span x=\"1\"" + space + "class='hljs-string'>", "<span class=\"a" + space + "b" + space + "hljs-title.function" + space + "hljs-string\">", "<span" + space + ">",
		} {
			probes = append(probes, highlightHTMLProbe{HTML: tag + "t</span>u", Theme: []string{"default", "keyword", "string", "title"}})
		}
	}
	for _, tag := range []string{
		`<span class="hljs-keyword`, `<span class='hljs-keyword`, `<span class="hljs-keyword'>`, `<span class='hljs-keyword">`, `<span class="a'b hljs-keyword">`, `<span class='a"b hljs-string'>`, `<span CLASS="hljs-keyword">`, `<span data-class="hljs-keyword">`,
		`<span classx="hljs-keyword">`, `<span class="hljs-keyword" class="hljs-string">`, `<span x="class='hljs-string'" class="hljs-keyword">`, `<span class="hljs-keyword"class="hljs-string">`, `<span class = 'hljs-string' >`, `<span class==  "hljs-string">`,
		`<span class>`, `<span class=>`, `<span class="">`, `<span class="   ">`, `<span class="hljs-">`, `<span class="hljs-.x">`, `<span class="hljs--x">`, `<span class="hljs-a.b-c">`, `<span class="hljs-a-b.c">`, `<span class="hljs-title.">`, `<span class="hljs-title-">`,
		`<span class="hljs-.">`, `<span class="Hljs-keyword">`, `<span class="xhljs-keyword">`, `<span class="hljs-keyword hljs-string">`, `<span class="hljs-string hljs-keyword">`, `<span class="meta hljs-meta-keyword">`,
	} {
		for _, theme := range [][]string{{"default", "keyword", "string", "title", "meta"}, {"title"}, {"meta"}, nil} {
			probes = append(probes, highlightHTMLProbe{HTML: tag + "t</span>u", Theme: theme}, highlightHTMLProbe{HTML: `<span class="hljs-string">` + tag + "t</span>u</span>v", Theme: theme})
		}
	}
	// Character references: the table form of every case parseInt and fromCodePoint distinguish, and the 16-unit limit on the reference.
	entities := []string{"amp", "lt", "gt", "quot", "apos", "AMP", "nbsp", "#x41", "#X41", "#65", "#x0041", "#0x41", "#x0x41", "#x+41", "#x-41", "#x-0", "# 65", "#\u00a065", "#\u2028 65", "#\ufeff65", "#\u180e65", "#+65", "#-0", "#-65", "#65abc", "#xZZ", "#x4G", "#x4g1", "#1e3", "#6.5",
		"#99999999999999999999", "#x10FFFF", "#x110000", "#x10ffff", "#xD7FF", "#xD800", "#xDBFF", "#xDC00", "#xDFFF", "#xE000", "#0", "#00065", "#", "#x", "#X", "#xx", "#1_0", "#x1_0", "#٣", "#ａ", "#x41 ", "#x 41", "#b1", "#o7", "#xfFfF", "#xABCDEF", "#xabcdef", "#9", "#09", "#x9", "#xA", "#xa", "#xG", "#xg", "#/", "#:", "#@", "#`", "#x/", "#x:", "#x@", "#x`", "#xFG", "#x0x", "#X0X", "#x0x0", "#x0X41", "#x-0x41", "#x+0x41", "#x0", "#x00", "#0x", "#x1x"}
	for _, pad := range []string{"#x", "#X", "#"} {
		for width := 1; width <= 20; width++ {
			digit := "41"
			if pad == "#" {
				digit = "65"
			}
			entities = append(entities, pad+strings.Repeat("0", width)+digit)
		}
	}
	for width := 1; width <= 20; width++ {
		entities = append(entities, strings.Repeat("a", width), "#\u00a0"+strings.Repeat("\u00a0", width)+"65")
	}
	for _, entity := range entities {
		probes = append(probes, highlightHTMLProbe{HTML: "a&" + entity + ";b", Theme: []string{"default"}}, highlightHTMLProbe{HTML: `<span class="hljs-keyword">&` + entity + `;;</span>&` + entity, Theme: []string{"keyword"}})
	}
	// Surrogate references: a high and a low half written one after the other join into one character, apart (a scope between them) they stay lone halves.
	halves := []string{"D800", "D83D", "DBFF", "DC00", "DE00", "DFFF", "D7FF", "E000"}
	for _, high := range halves {
		for _, low := range halves {
			probes = append(probes, highlightHTMLProbe{HTML: "&#x" + high + ";&#x" + low + ";", Theme: []string{"default"}}, highlightHTMLProbe{HTML: "&#x" + high + `;<span class="hljs-keyword">&#x` + low + ";</span>", Theme: []string{"keyword"}})
		}
	}
	// Every scope reading against the full theme and against none.
	for _, open := range opens {
		for _, theme := range [][]string{nil, keys, {"default"}} {
			probes = append(probes, highlightHTMLProbe{HTML: open + "a\nb</span>c", Theme: theme})
		}
	}
	return probes
}

func renderHighlightedHTMLProbe(probe highlightHTMLProbe) highlightHTMLResult {
	theme := HighlightTheme{}
	for _, key := range probe.Theme {
		theme[key] = func(text string) string { return "<" + key + ">" + text + "</" + key + ">" }
	}
	out := RenderHighlightedHtml(probe.HTML, theme)
	return highlightHTMLResult{Out: &out}
}

// renderHighlightedHtml (utils/syntax-highlight.ts) against pinned Pi: a run of text takes the formatter of its innermost span whose scope the theme maps
// (exact, then the part before a dot, then the part before a dash), else the theme's default; each non-empty line is formatted alone; character references
// are decoded; stray and unclosed tags are kept or dropped as Pi does.
func TestRenderHighlightedHtmlMatchesPi(t *testing.T) {
	probes := highlightHTMLProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_highlighted_html.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []highlightHTMLResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, probe := range probes {
		got := renderHighlightedHTMLProbe(probe)
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 5 {
				g, _ := json.Marshal(got)
				e, _ := json.Marshal(expected[i])
				p, _ := json.Marshal(probe)
				t.Errorf("%s:\n  Pig %s\n  Pi  %s", p, g, e)
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestRenderHighlightedHtmlProbeDump prints the corpus for the Pi side of the render-highlighted-html parity scenario.
func TestRenderHighlightedHtmlProbeDump(t *testing.T) {
	line, err := json.Marshal(highlightHTMLProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("hlhtml-probes:%s\n", line)
}

// TestRenderHighlightedHtmlParity prints Pig's output for the corpus, one JSON line per probe, for the render-highlighted-html parity scenario.
func TestRenderHighlightedHtmlParity(t *testing.T) {
	for _, probe := range highlightHTMLProbes() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		result := renderHighlightedHTMLProbe(probe)
		if err := encoder.Encode(result); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("hlhtml-observation:%s", line.String())
	}
}
