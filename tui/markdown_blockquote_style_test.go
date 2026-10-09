package tui

import (
	"reflect"
	"strings"
	"testing"
)

// upstreamProbeMarkdownTheme mirrors coding-agent getMarkdownTheme's shape:
// colors through theme.fg (open + text + "\x1b[39m", no inner-close
// rewriting) and decorations through chalk.
func upstreamProbeMarkdownTheme() *MarkdownTheme {
	fg := func(open string) func(string) string {
		return func(s string) string { return markdownForeground(open, s) }
	}
	gray, cyan := "\x1b[38;2;128;128;128m", "\x1b[38;2;138;190;183m"
	return &MarkdownTheme{
		Heading: fg("\x1b[38;2;240;198;116m"), Link: fg("\x1b[38;2;129;162;190m"), LinkUrl: fg("\x1b[38;2;102;102;102m"),
		Code: fg(cyan), CodeBlock: fg("\x1b[38;2;181;189;104m"), CodeBlockBorder: fg(gray),
		Quote: fg(gray), QuoteBorder: fg(gray), Hr: fg(gray), ListBullet: fg(cyan),
		Bold:          func(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) },
		Italic:        func(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) },
		Strikethrough: func(s string) string { return markdownDecoration("\x1b[9m", SGRStrikeReset, s) },
		Underline:     func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
	}
}

// upstream: markdown.ts blockquote + paragraph rendering with an italic
// defaultTextStyle. Expected rows were captured by running the pinned
// .upstream/current/packages/tui/src sources under Node 24 (marked 18.0.11,
// chalk 6.0.0 at level 3) with the same theme: new Markdown(text, 0, 0,
// theme, {italic: true}).render(width).
func TestMarkdownBlockquoteDefaultStyleMatchesUpstreamBytes(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		width      int
		want       []string
	}{
		{
			// marked nests **c** inside *b … d*: strong closes with 22 and the
			// emphasis stays open; nothing re-opens italic around c.
			name: "strong nested in emphasis", text: "> a *b **c** d* e", width: 30,
			want: []string{"\x1b[38;2;128;128;128m│ \x1b[39m\x1b[38;2;128;128;128m\x1b[3ma \x1b[3mb \x1b[1mc\x1b[22m\x1b[38;2;128;128;128m\x1b[3m d\x1b[23m\x1b[3m\x1b[38;2;128;128;128m\x1b[3m e\x1b[23m\x1b[39m                   "},
		},
		{
			// A blockquote interrupting a paragraph is the paragraph's next
			// token, so the paragraph adds its spacing row.
			name: "paragraph interrupted by blockquote", text: "text\n> quote", width: 20,
			want: []string{
				"\x1b[3mtext\x1b[23m                ",
				"                    ",
				"\x1b[38;2;128;128;128m│ \x1b[39m\x1b[38;2;128;128;128m\x1b[3mquote\x1b[23m\x1b[39m             ",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			md := NewMarkdownWithOptions(tc.text, 0, 0, upstreamProbeMarkdownTheme(), &DefaultTextStyle{Italic: true}, &MarkdownOptions{})
			if got := md.Render(tc.width); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Render(%d) =\n%q\nwant\n%q", tc.width, got, tc.want)
			}
		})
	}
}

// upstream: markdown.ts paragraph adds a spacing row before every next token
// except space and list. Captured from the pinned sources as above, with
// identity theme functions and no defaultTextStyle, render(20) trimmed.
func TestMarkdownParagraphSpacingBeforeInterruptingBlockMatchesUpstream(t *testing.T) {
	id := func(s string) string { return s }
	theme := &MarkdownTheme{Heading: id, Link: id, LinkUrl: id, Code: id, CodeBlock: id, CodeBlockBorder: id, Quote: id, QuoteBorder: id, Hr: id, ListBullet: id, Bold: id, Italic: id, Strikethrough: id, Underline: id}
	for text, want := range map[string][]string{
		"text\n> q":              {"text", "", "│ q"},
		"text\n# h":              {"text", "", "h"},
		"text\n***":              {"text", "", "────────────────────"},
		"text\n$$\nx\n$$":        {"text", "", "x"},
		"text\n- l":              {"text", "- l"},
		"text\n#tag":             {"text", "#tag"},
		"text\n```\nc\n```":      {"text", "", "```", "  c", "```"},
		"a\n| x |\n| - |\n| y |": {"a", "", "┌───┐", "│ x │", "├───┤", "│ y │", "└───┘"},
	} {
		got := NewMarkdownWithOptions(text, 0, 0, theme, nil, &MarkdownOptions{}).Render(20)
		for i := range got {
			got[i] = strings.TrimRight(got[i], " ")
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: Render(20) = %q, want %q", text, got, want)
		}
	}
}

// upstream: markdown.ts with coding-agent getMarkdownTheme's chalk italic and
// bold (inner closes re-open, line breaks close and re-open) and theme.fg
// colors. Expected rows were captured by running the pinned
// .upstream/current/packages/tui/src sources under Node 24 (marked 18.0.11,
// chalk 6.0.0 at level 3): new Markdown(text, 1, 0, theme, style).render(30).
func TestMarkdownEmphasisAndSetextMatchUpstreamBytes(t *testing.T) {
	fg := func(open string) func(string) string {
		return func(s string) string { return markdownForeground(open, s) }
	}
	quote, text := fg("\x1b[38;2;157;165;169m"), fg("\x1b[38;2;222;224;225m")
	theme := &MarkdownTheme{
		Heading: fg("\x1b[38;2;205;154;34m"), Link: quote, LinkUrl: quote, Code: quote, CodeBlock: quote, CodeBlockBorder: quote,
		Quote: quote, QuoteBorder: quote, Hr: quote, ListBullet: quote,
		Bold:          func(s string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, s) },
		Italic:        func(s string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, s) },
		Strikethrough: func(s string) string { return markdownDecoration("\x1b[9m", SGRStrikeReset, s) },
		Underline:     func(s string) string { return markdownDecoration("\x1b[4m", SGRUnderlineReset, s) },
	}
	for _, tc := range []struct {
		name, text string
		style      *DefaultTextStyle
		want       []string
	}{
		{
			name: "quote with soft break", text: "> a *quoted* line\n> and **more**", style: &DefaultTextStyle{Color: text},
			want: []string{
				" \x1b[38;2;157;165;169m│ \x1b[39m\x1b[38;2;157;165;169m\x1b[3ma \x1b[3mquoted\x1b[23m\x1b[3m\x1b[38;2;157;165;169m\x1b[3m line\x1b[23m              ",
				" \x1b[38;2;157;165;169m│ \x1b[39m\x1b[38;2;157;165;169m\x1b[3mand \x1b[1mmore\x1b[22m\x1b[23m\x1b[39m                   ",
			},
		},
		{
			name: "strong nested in emphasis", text: "Some *emphasis with **strong** inside* text", style: &DefaultTextStyle{Color: text, Italic: true},
			want: []string{
				" \x1b[3m\x1b[38;2;222;224;225mSome \x1b[39m\x1b[23m\x1b[3m\x1b[3m\x1b[38;2;222;224;225memphasis with \x1b[39m\x1b[23m\x1b[3m\x1b[1m\x1b[3m\x1b[38;2;222;224;225mstrong\x1b[39m\x1b[23m\x1b[3m\x1b[22m\x1b[3m\x1b[38;2;222;224;225m\x1b[3m\x1b[38;2;222;224;225m    ",
				" \x1b[3;38;2;222;224;225minside\x1b[39m\x1b[23m\x1b[3m\x1b[23m\x1b[3m\x1b[38;2;222;224;225m\x1b[3m\x1b[38;2;222;224;225m text\x1b[39m\x1b[23m                  ",
			},
		},
		{
			name: "setext level 2", text: "more\n---", style: &DefaultTextStyle{Color: text},
			want: []string{" \x1b[38;2;205;154;34m\x1b[1mmore\x1b[22m\x1b[39m                         "},
		},
		{
			name: "setext level 2 between paragraphs", text: "para\n\nmore\n---\nafter",
			want: []string{" para                         ", strings.Repeat(" ", 30), " \x1b[38;2;205;154;34m\x1b[1mmore\x1b[22m\x1b[39m                         ", strings.Repeat(" ", 30), " after                        "},
		},
		{
			name: "setext level 1", text: "x\n===",
			want: []string{" \x1b[38;2;205;154;34m\x1b[1m\x1b[4mx\x1b[24m\x1b[22m\x1b[39m                            "},
		},
		{
			// marked's lheading rule does not stop at a thematic break, so the
			// whole run up to the underline is one heading.
			name: "setext heading spans a rule line", text: "more\n***\nsetext\n---",
			want: []string{
				" \x1b[38;2;205;154;34m\x1b[1mmore\x1b[22m\x1b[39m                         ",
				" \x1b[38;2;205;154;34m\x1b[1m***\x1b[22m\x1b[39m                          ",
				" \x1b[38;2;205;154;34m\x1b[1msetext\x1b[22m\x1b[39m                       ",
			},
		},
		{
			// A "*" run followed by whitespace is not left-flanking and opens nothing.
			name: "spaced opener stays text", text: "a ** b**",
			want: []string{" a ** b**                     "},
		},
		{
			// A "*" run after whitespace cannot close.
			name: "spaced closer stays text", text: "**a ** b",
			want: []string{" **a ** b                     "},
		},
		{
			name: "empty strong stays text", text: "x****y",
			want: []string{" x****y                       "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewMarkdownWithOptions(tc.text, 1, 0, theme, tc.style, &MarkdownOptions{}).Render(30); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Render(30) =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// upstream: marked's blockquote tokenizer applies blockquoteSetextReplace to a
// lazy continuation line, so a lazy "===" or "---" underline continues the
// quoted paragraph instead of closing a setext heading; an underline carrying
// its own ">" still closes one. Captured from pinned pi-tui 1.0.4 (marked
// 18.0.11) with identity theme functions: new Markdown(text, 0, 0,
// theme).render(20), trimmed.
func TestMarkdownLazyBlockquoteSetextUnderlineMatchesUpstream(t *testing.T) {
	id := func(s string) string { return s }
	theme := &MarkdownTheme{Heading: id, Link: id, LinkUrl: id, Code: id, CodeBlock: id, CodeBlockBorder: id, Quote: id, QuoteBorder: id, Hr: id, ListBullet: id, Bold: id, Italic: id, Strikethrough: id, Underline: id}
	for text, want := range map[string][]string{
		"> a\n===":       {"│ a", "│ ==="},
		"a\n> b\n===":    {"a", "", "│ b", "│ ==="},
		"> a\n  ===":     {"│ a", "│   ==="},
		"> a\n> ===":     {"│ a"},
		"> a\n> b\n> --": {"│ a", "│ b"},
	} {
		got := NewMarkdownWithOptions(text, 0, 0, theme, nil, &MarkdownOptions{}).Render(20)
		for i := range got {
			got[i] = strings.TrimRight(got[i], " ")
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: Render(20) = %q, want %q", text, got, want)
		}
	}
}
