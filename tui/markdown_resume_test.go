package tui

import (
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// resumeBlocks are the block kinds a generated document is built from. Each has variants that differ in how a block ends or
// continues: blank lines inside, lazy lines, loose and nested lists, unclosed fences and HTML, definitions, LaTeX.
var resumeBlocks = []func(rng *rand.Rand, n int) string{
	func(rng *rand.Rand, n int) string {
		return strings.Repeat("A paragraph "+strconv.Itoa(n)+" with **bold**, `code`, _em_ and http://a.b/"+strconv.Itoa(n)+" that wraps when it is long enough to. ", 1+rng.IntN(4))
	},
	func(rng *rand.Rand, n int) string {
		return "A line\nof the same paragraph\nand a third " + strconv.Itoa(n)
	},
	func(rng *rand.Rand, n int) string {
		return strings.Repeat("#", 1+rng.IntN(6)) + " Heading " + strconv.Itoa(n)
	},
	func(rng *rand.Rand, n int) string {
		return "Setext " + strconv.Itoa(n) + "\n" + []string{"===", "---", "-"}[rng.IntN(3)]
	},
	func(rng *rand.Rand, n int) string { return []string{"---", "***", "___", "- - -"}[rng.IntN(4)] },
	func(rng *rand.Rand, n int) string {
		return "- item " + strconv.Itoa(n) + "\n- second `code`\n  - nested **x**\n  - nested y\n- third"
	},
	func(rng *rand.Rand, n int) string {
		return "1. first " + strconv.Itoa(n) + "\n\n2. loose second\n\n   continued paragraph\n3. third"
	},
	func(rng *rand.Rand, n int) string { return "* star\n\n\n* after two blanks\n    indented lazy" },
	func(rng *rand.Rand, n int) string {
		fence := []string{"```", "~~~", "````"}[rng.IntN(3)]
		return fence + []string{"go", "ts", "", "python"}[rng.IntN(4)] + "\nfunc f" + strconv.Itoa(n) + "() {\n\n\treturn 1\n}\n" + fence
	},
	func(rng *rand.Rand, n int) string { return "```\nnever closed " + strconv.Itoa(n) + "\n\nstill code" },
	func(rng *rand.Rand, n int) string {
		return "    indented code " + strconv.Itoa(n) + "\n\n    more after a blank\n\tand a tab"
	},
	func(rng *rand.Rand, n int) string {
		return "> quote " + strconv.Itoa(n) + "\nlazy continuation\n> - list in quote\n>\n> after blank"
	},
	func(rng *rand.Rand, n int) string {
		return "| a | b |\n|---|:-:|\n| 1 | two " + strconv.Itoa(n) + " |\n| 3 | four |"
	},
	func(rng *rand.Rand, n int) string { return "a | b\n--|--\n1 | 2" },
	func(rng *rand.Rand, n int) string { return "<div>\nhtml " + strconv.Itoa(n) + "\n</div>" },
	func(rng *rand.Rand, n int) string { return "<pre>\nraw\n\nmore raw</pre>\n" },
	func(rng *rand.Rand, n int) string { return "<!-- comment " + strconv.Itoa(n) + "\n\nspanning -->" },
	func(rng *rand.Rand, n int) string { return "<a href=\"u\">open " + strconv.Itoa(n) },
	func(rng *rand.Rand, n int) string { return "close</a> http://c.d" },
	func(rng *rand.Rand, n int) string { return "$$\nx^2 + " + strconv.Itoa(n) + "\n$$" },
	func(rng *rand.Rand, n int) string { return "\\[\na_" + strconv.Itoa(n) + "\n\\]" },
	func(rng *rand.Rand, n int) string { return "inline $x_" + strconv.Itoa(n) + "$ math and $$y$$" },
	func(rng *rand.Rand, n int) string {
		return "see [ref" + strconv.Itoa(n%3) + "] and [text](http://x.y) www.z.w"
	},
	func(rng *rand.Rand, n int) string {
		return "[ref" + strconv.Itoa(n%3) + "]: http://r.s/" + strconv.Itoa(n) + " \"a title\""
	},
	func(rng *rand.Rand, n int) string { return "[ref0]:\n  http://r.s\n  'multi\n  line title'" },
	func(rng *rand.Rand, n int) string { return "# " },
	func(rng *rand.Rand, n int) string { return "trailing spaces  \nhard break\\\nbackslash" },
	func(rng *rand.Rand, n int) string { return "日本語 😀 café " + strings.Repeat("wide ", 12) },
}

// resumeBarrierBlocks are blocks whose meaning depends on text far after them (a LaTeX or definition start that a later line
// may complete) or that define links, so a document holding one is not resumed past it.
var resumeBarrierBlocks = []func(rng *rand.Rand, n int) string{
	func(rng *rand.Rand, n int) string { return "dangling\n$$ x_" + strconv.Itoa(n) + " and more text" },
	func(rng *rand.Rand, n int) string { return "dangling\n\\[ y_" + strconv.Itoa(n) },
	func(rng *rand.Rand, n int) string { return "closing $$ z" },
	func(rng *rand.Rand, n int) string { return "a$$ plain words " + strconv.Itoa(n) },
	func(rng *rand.Rand, n int) string { return "plain end $$" },
	func(rng *rand.Rand, n int) string {
		return "[ref" + strconv.Itoa(n%3) + "]: http://t.u \"unterminated title " + strconv.Itoa(n)
	},
	func(rng *rand.Rand, n int) string { return "she said \"hello\" and 'bye' (twice)" },
	func(rng *rand.Rand, n int) string {
		return "> [ref" + strconv.Itoa(n%3) + "]: http://q.r/" + strconv.Itoa(n) + "\n> quoted text"
	},
	func(rng *rand.Rand, n int) string { return "- [ref" + strconv.Itoa(n%3) + "]: http://l.m\n- item" },
	func(rng *rand.Rand, n int) string { return "[bracket\n\ntext ] and [x" },
}

// resumeDocument builds a document of random blocks with random separators and line endings.
func resumeGeneratedDocument(rng *rand.Rand, barriers bool) string {
	pool := resumeBlocks
	if barriers {
		pool = slices.Concat(resumeBlocks, resumeBarrierBlocks)
	}
	separators := []string{"\n\n", "\n\n", "\n\n", "\n", "\n\n\n", "\n \n", "\n\t\n"}
	var doc strings.Builder
	for n, count := 0, 4+rng.IntN(30); n < count; n++ {
		doc.WriteString(pool[rng.IntN(len(pool))](rng, n))
		doc.WriteString(separators[rng.IntN(len(separators))])
	}
	text := doc.String()
	switch rng.IntN(6) {
	case 0:
		text = strings.ReplaceAll(text, "\n", "\r\n")
	case 1:
		text = strings.TrimRight(text, "\n")
	}
	return text
}

// A Markdown that streams a document, as a message grows by a few bytes per frame, renders every frame as a new component
// given the same text does. The streamed component resumes each render from the last top-level block the new text cannot
// have changed; Pi's Markdown renders every block again (markdown.ts render), so a new component is the reference. Documents
// mix every block kind, with the endings, continuations and look-ahead that make a block depend on what follows it; chunks
// split lines and delimiters anywhere; the text is sometimes replaced by one that is not an extension, and the width,
// padding and theme change.
func TestResumedMarkdownEqualsAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	rng := rand.New(rand.NewPCG(11, 12))
	resumed, frames := 0, 0
	documents := 150
	if testing.Short() {
		documents = 40
	}
	for index := range documents {
		document := resumeGeneratedDocument(rng, index%2 == 1)
		paddingX, paddingY := rng.IntN(3), rng.IntN(2)
		width := 12 + rng.IntN(60)
		streamed := NewMarkdownWithOptions("", paddingX, paddingY, nil, nil, nil)
		text := ""
		for end := 0; end < len(document); {
			end = min(len(document), end+1+rng.IntN(1+rng.IntN(24)))
			next := document[:end]
			switch rng.IntN(40) {
			case 0:
				// A replacement that is not an extension: the earlier points must not be reused.
				next = document[:rng.IntN(end+1)] + "REPLACED\n\n" + document[:end]
			case 1:
				width = 12 + rng.IntN(60)
			}
			if streamed.resume != nil && strings.HasPrefix(next, text) && streamed.resume.pick(next) != nil {
				resumed++
			}
			text = next
			streamed.SetText(text)
			got := streamed.Render(width)
			want := NewMarkdownWithOptions(text, paddingX, paddingY, nil, nil, nil).Render(width)
			frames++
			if !slices.Equal(got, want) {
				t.Fatalf("document %d, %d bytes of %q at width %d:\n got %q\nwant %q", index, len(text), document, width, got, want)
			}
		}
	}
	// The test proves nothing if it never resumes.
	if resumed < frames/5 {
		t.Fatalf("only %d of %d frames resumed from a point", resumed, frames)
	}
	t.Logf("%d of %d frames resumed", resumed, frames)
}

// resumeDependentDocuments are documents in which a block's rendering depends on text that arrives after it, or on state the
// blocks before it left, in ways a resume that is too eager gets wrong. Each is streamed a byte at a time.
var resumeDependentDocuments = []string{
	// A paragraph with a LaTeX start line is clipped there once a closing delimiter arrives, however far on.
	"dangling\n$$ x and more\n\nsecond\n\nthird\n\nfourth\n\nfifth $$ end\n\nsixth\n\nseventh\n",
	"dangling\n\\[ y\n\nsecond\n\nthird\n\nfourth\n\nfifth \\] end\n\nsixth\n\nseventh\n",
	"dangling\n$$\nx\n\nsecond\n\nthird\n\nfourth\n$$\nfifth\n\nsixth\n\nseventh\n",
	// A paragraph whose line has "$$" or "\[" right after its first character is clipped there once a closing delimiter
	// arrives (Lexer.ts:241-270 with the latexBlock start() of markdown.ts:127-130).
	"a$$ hello world\n\nsecond\n\nthird\n\nfourth\n\nfifth $$\n\nsixth\n\nseventh\n",
	"a$$\nhello world\n\nsecond\n\nthird\n\nfourth\n$$\nfifth\n\nsixth\n\nseventh\n",
	"é $$ words\n\nsecond\n\nthird\n\nfourth\n\nfifth $$\n\nsixth\n\nseventh\n",
	// A paragraph clipped before a LaTeX start right after its first character (Lexer.ts:241-270): the start is pending like
	// one at the start of a line, and the rest of the clipped line, lexed next, is no block start.
	"a\n\nx$$ y\n\nsecond\n\nthird\n\nfourth $$ end\n\nfifth\n\nsixth\n\nseventh\n",
	"\nx$$$$$\n\n1. c- a$$* b> \n \n\nafter\n\nmore\n",
	// A definition whose title or label closes later.
	"[r]: http://t.u \"unterminated\n\nsecond\n\nthird\n\nfourth \"closes\" here\n\nfifth\n\nsixth\n",
	"[bracket\n\nsecond\n\nthird ] and [x\n\nfourth\n\nfifth\n",
	"[r]: http://t.u\n\"title on the next line\"\n\nsecond\n\nthird\n\nfourth\n",
	// A definition resolves references before and after it, also when it sits in a quote or list item.
	"first [r] and [q]\n\nsecond\n\n> [r]: http://x.y\n\nthird [r]\n\nfourth [q] [r]\n\nfifth\n",
	"> [r]: http://x.y\n\nfirst\n\nsecond [r]\n\nthird [r] www.a.b\n\nfourth\n",
	"- [r]: http://x.y\n- item\n\nsecond [r]\n\nthird [r]\n\nfourth\n",
	"see [r]\n\nsecond\n\nthird\n\n[r]: http://x.y\n\nfourth [r]\n\nfifth\n",
	// Inline lexer state that crosses blocks: an open tag suppresses autolinks until it closes.
	"<a href=\"u\">open http://a.b\n\nsecond http://c.d\n\nthird www.e.f\n\nclose</a> http://g.h\n\nfifth www.i.j\n\nsixth\n",
	"before [x] http://a.b\n\nsecond <a href=\"u\">x\n\nthird http://c.d\n\nfourth</a>\n\nfifth http://e.f\n\nsixth\n",
	// Blocks that continue across blank lines or run to the end of the text until a closer arrives.
	"<pre>\nunclosed\n\nsecond\n\nthird\n\nfourth </pre>\n\nfifth\n\nsixth\n\nseventh\n",
	"<!-- comment\n\nsecond\n\nthird\n\nfourth -->\n\nfifth\n\nsixth\n\nseventh\n",
	"```go\ncode\n\nsecond\n\nthird\n\nfourth\n```\n\nfifth\n\nsixth\n\nseventh\n",
	"- a\n\n- b\n\n  continued\n\nafter\n\nlater\n\nmore\n\nand more\n",
	"- a\n\n    indented after a list\n\nafter\n\nlater\n\nmore\n",
	"    code\n\n    more code\n\nnot code\n\nlater\n\nmore\n\nand more\n",
	"1. a\n\n2. b\n\n   - nested\n\n     para\n\nafter\n\nlater\n\nmore\n",
	"| a | b |\n|---|---|\n| 1 | 2 |\n\n| c |\n|---|\n| 3 |\n\nafter\n\nlater\n\nmore\n",
	"para\n| a | b |\n|---|---|\n| 1 | 2 |\n\nafter\n\nlater\n\nmore\n",
	"Setext\n===\n\nSetext two\n---\n\nplain\n---\n\nafter\n\nlater\n\nmore\n",
	// State the blocks before a point leave for the end of the text: empty headings, quotes that render nothing, lists whose
	// last line holds spaces, a list that a fence follows directly.
	"# \n\n>\n\n>\n\n>\n\n>\n",
	"a\n\n# \n\n## \n\nb\n\n>\n\n>\n\n>\n",
	"- a  \n\n>\n\n>\n\n>\n\n>\n",
	"- a\n\n```\ncode\n```\n\nx\n\ny\n\nz\n\nw\n",
	"* a\n\n* b  \n\n```\ncode\n```\n\nx\n\ny\n\nz\n\nw\n",
	"\n\n\nleading blanks\n\nsecond\n\nthird\n\nfourth\n\nfifth\n",
	"first\n\n\n\n\nmany blanks\n\n \n\nspaces\n\n\t\n\ntab\n\nlast\n",
	"first\r\n\r\nsecond\r\n\r\nthird\r\n\r\nfourth\r\n\r\nfifth\r\n\r\nsixth",
	"a\n\n\tx\n\nb\n\n\ty\n\nc\n\nd\n\ne\n",
	"para\n\n<div>\nhtml\n</div>\n\nafter\n\nmore\n\nlater\n\nmore\n",
}

func TestResumedMarkdownEqualsAFreshRenderForDependentBlocks(t *testing.T) {
	mdCaseSetup(t)
	resumed := 0
	for index, document := range resumeDependentDocuments {
		for _, geometry := range [][3]int{{40, 0, 0}, {13, 1, 1}} {
			width, paddingX, paddingY := geometry[0], geometry[1], geometry[2]
			streamed := NewMarkdownWithOptions("", paddingX, paddingY, nil, nil, nil)
			for end := 0; end <= len(document); end++ {
				text := document[:end]
				if streamed.resume != nil && streamed.resume.pick(text) != nil {
					resumed++
				}
				streamed.SetText(text)
				got := streamed.Render(width)
				want := NewMarkdownWithOptions(text, paddingX, paddingY, nil, nil, nil).Render(width)
				if !slices.Equal(got, want) {
					t.Fatalf("document %d at width %d, %q:\n got %q\nwant %q", index, width, text, got, want)
				}
			}
		}
	}
	if resumed == 0 {
		t.Fatal("no render resumed from a point")
	}
}

// A change of what the rows are a function of between two frames of a stream (the theme, hyperlink support, the default
// color, the width) starts a new render of all of the text, never one that resumes with rows of the old inputs.
func TestResumedMarkdownRendersAgainWhenItsInputsChange(t *testing.T) {
	mdCaseSetup(t)
	active := ActiveTheme()
	t.Cleanup(func() { activeTheme.Store(active) })
	document := strings.Repeat("A [link](https://a.b) and http://c.d in **bold** `code` text that is long enough to wrap.\n\n- item one\n- item two\n\n", 12)
	// Each step starts from the built-in dark theme, not from the theme an earlier test left active. The Pi-oracle tests leave the light theme active, and a restore under a terminal without truecolor stores a 256-color copy of it, not the built-in the
	// change below stores. Telling the change from a no-op by comparing theme pointers then fails, and the light theme replaces itself: no row changes.
	baseline := builtinDarkTheme()
	changes := []func(*Markdown){
		func(*Markdown) { storeActiveTheme(builtinLightTheme()) },
		func(*Markdown) { SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true}) },
		func(m *Markdown) { m.SetDefaultColor("\x1b[38;5;244m") },
	}
	for index, change := range changes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		storeActiveTheme(baseline)
		streamed := NewMarkdownWithOptions("", 1, 0, nil, nil, nil)
		var color string
		fresh := func(text string) []string {
			md := NewMarkdownWithOptions(text, 1, 0, nil, nil, nil)
			if color != "" {
				md.SetDefaultColor(color)
			}
			return md.Render(50)
		}
		changed := false
		for end := 0; end <= len(document); end += 7 {
			if end >= len(document)/2 && !changed {
				changed = true
				before := fresh(document[:end])
				change(streamed)
				color = streamed.defaultColor
				// The first paragraph never changes, so only the changed input can change its row.
				if slices.Equal(before[:1], fresh(document[:end])[:1]) {
					t.Fatalf("change %d does not alter the first row, so the step proves nothing", index)
				}
			}
			streamed.SetText(document[:end])
			if got, want := streamed.Render(50), fresh(document[:end]); !slices.Equal(got, want) {
				t.Fatalf("change %d, %d bytes:\n got %q\nwant %q", index, end, got, want)
			}
		}
	}
}

// A long answer streamed 24 bytes at a time renders every sampled frame as a new component does, and nearly every frame
// resumes from a point.
func TestResumedMarkdownLongAnswerEqualsAFreshRender(t *testing.T) {
	mdCaseSetup(t)
	answer := longAnswer(30000)
	streamed := NewMarkdownWithOptions("", 0, 0, nil, nil, nil)
	frames, resumed := 0, 0
	for end := 24; ; end += 24 {
		end = min(end, len(answer))
		text := answer[:end]
		if streamed.resume != nil && streamed.resume.pick(text) != nil {
			resumed++
		}
		streamed.SetText(text)
		got := streamed.Render(100)
		frames++
		if frames%9 == 0 || end == len(answer) {
			if want := NewMarkdown(text).Render(100); !slices.Equal(got, want) {
				t.Fatalf("%d bytes:\n got %q\nwant %q", end, got, want)
			}
		}
		if end == len(answer) {
			break
		}
	}
	if resumed < frames*9/10 {
		t.Fatalf("%d of %d frames resumed", resumed, frames)
	}
}
