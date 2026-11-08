package tui

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// compareMarkdownTextsWithPi requires the rows of every text to equal Pi's, at several widths and paddings, with the lone surrogates carried as
// JavaScript holds them. Pi measures a lone surrogate as no column and writes it as one replacement character, so a row that holds one wraps and
// pads differently from a row that holds three U+FFFD.
func compareMarkdownTextsWithPi(t *testing.T, texts []string) {
	t.Helper()
	type probe struct {
		Text     string `json:"text"`
		PaddingX int    `json:"paddingX"`
		Width    int    `json:"width"`
	}
	var probes []probe
	for _, text := range texts {
		for _, width := range []int{6, 9, 14, 40} {
			probes = append(probes, probe{text, 0, width}, probe{text, 1, width})
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
	differing := map[string]bool{}
	for i, p := range probes {
		got := NewMarkdownWithOptions(p.Text, p.PaddingX, 0, nil, nil, nil).Render(p.Width)
		if strings.Join(got, "\n") != strings.Join(expected[i], "\n") && !differing[p.Text] {
			differing[p.Text] = true
			t.Errorf("%q at width %d (padding %d):\n  Pig %q\n  Pi  %q", p.Text, p.Width, p.PaddingX, got, expected[i])
		}
	}
	if len(differing) > 0 {
		t.Errorf("%d of %d texts differ from Pi", len(differing), len(texts))
	}
}

// Every inline kind and block that holds a lone surrogate renders as Pi does: paragraph, heading, emphasis, strikethrough, code span, link and image
// (text and destination), autolink, bare URL, email, escape, entity, inline HTML, hard break, table, list, quote, and link reference definitions
// (label, destination and title) with the references that use them, and math (inline, display and bracketed, with the unit in a script, a fraction and
// text). Not covered: HTML comments.
func TestMarkdownInlineLoneSurrogatesMatchPi(t *testing.T) {
	high, low := jsstring.FromUTF16([]uint16{0xd83d}), jsstring.FromUTF16([]uint16{0xde00})
	var texts []string
	for _, h := range []string{high, low} {
		texts = append(texts,
			"text "+h+"aaaa", "a"+h+"b", h, h+h, "x "+h+" y "+h, "word"+h+"word and more words to wrap around the narrow widths",
			"**bold"+h+"** and *it"+h+"* and ~~del"+h+"~~ and `code"+h+"` and ***both"+h+"***", "**"+h+"bold** _"+h+"it_", "*a"+h+"* b", "a*"+h+"*b",
			"# head"+h+"\n\ntext", "## "+h+" two\n\ntext", "setext"+h+"\n=====\n\ntext", "###### deep"+h,
			"[link"+h+"](/url)", "[link](/url"+h+")", "[link](</url"+h+">)", "[link](/url \"title"+h+"\")", "![img"+h+"](/img)", "![img](/img"+h+")",
			"<http://x.com/"+h+">", "http://x.com/"+h+"path and more", "www.x.com/"+h, "mail"+h+"@x.com and a@x"+h+".com",
			"\\"+h, "a\\"+h+"b", "&amp;"+h+"&copy;", "&"+h+";", "<span "+h+">x</span>", "<b>"+h+"</b>", "line"+h+"  \nbreak"+h, "line\\\n"+h,
			"| a"+h+" | b |\n|---|---|\n| "+h+" | c"+h+" |", "| h |\n|---|\n| "+h+"cell with more text to wrap |",
			"- item"+h+"\n- "+h+" second\n  - nested"+h, "1. one"+h+"\n2. two", "- [ ] task"+h+"\n- [x] done"+h,
			"> quote"+h+"\n> > nested"+h+"\n> text", "> - item"+h+"\n>\n> para"+h,
			"[a][b"+h+"]\n\n[b"+h+"]: /u", "[b"+h+"][]\n\n[b"+h+"]: /u", "[b"+h+"]\n\n[b"+h+"]: /u \"title\"", "[foo]\n\n[foo]: http://x.com/"+h,
			"[foo]\n\n[foo]: /u \"title"+h+"\"", "[foo"+h+"]: /u\n\ntext "+h, "![a][b"+h+"]\n\n[B"+h+"]: /u", "[a][B"+h+"]\n\n[b"+h+"]: /u",
			"<div>"+h+"</div>\n\ntext", "para"+h+"\n\n    code"+h+"\n\n```\nfence"+h+"\n```", "a "+h+" b\n\n---\n\nc "+h,
			"$x"+h+"$", "$"+h+"$ and $a"+h+"+b$ more", "$x^{"+h+"}$ text", "$\\frac{"+h+"}{2}$", "$$\n"+h+"\n$$", "$$\n\\frac{"+h+"}{y}\n$$", "\\("+h+"\\)", "\\["+h+"\\]",
			"\\[\n\\begin{matrix} "+h+" & b \\\\ c & d \\end{matrix}\n\\]", "price $"+h+" and $x$ or $5",
			// A text that holds characters of the private-use area keeps its own.
			"\U0010F800\n\n```js\nx"+h+"y\n```", "\U0010F83D"+h+"\U0010FFFF text", "[a][b\U0010F83D"+h+"]\n\n[b\U0010F83D"+h+"]: /u",
		)
	}
	compareMarkdownTextsWithPi(t, texts)
}

// A high half and a low half that reach the Markdown as one Go string, from a plain concatenation of two JavaScript strings, are one character as in
// Pi, in a paragraph, a code span and a code fence alike.
func TestMarkdownAdjacentSurrogateHalvesAreOneCharacterAsInPi(t *testing.T) {
	high, low := jsstring.FromUTF16([]uint16{0xd83d}), jsstring.FromUTF16([]uint16{0xde00})
	compareMarkdownTextsWithPi(t, []string{
		high + low, "a " + high + low + " b", "`" + high + low + "`", "```\n" + high + low + "\n```", "```js\nx" + high + low + "y\n```",
		"**" + high + low + "** and " + high + low + low, high + high + low + low,
	})
}
