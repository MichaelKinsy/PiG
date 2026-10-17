//go:build !pig_strip_mermaid

package codingagent

import (
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	gmparser "github.com/MichaelKinsy/PiG/third_party/goldmark/parser"
	gmtext "github.com/MichaelKinsy/PiG/third_party/goldmark/text"
	"github.com/MichaelKinsy/PiG/third_party/goldmark/util"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/mermaid"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/src/modes/interactive/components/mermaid.ts.

var markedBlockSpace = lazyregexp.New(`^(?:[ \t]*(?:\n|$))+`)

// Parse block ownership without inline rendering. The fence adapter applies Marked's token boundaries to every fence, so containment and the replacement body cannot disagree. The parser is built on first use, not on every start.
var mermaidBlockParser = sync.OnceValue(newMermaidBlockParser)

func newMermaidBlockParser() gmparser.Parser {
	blocks := append([]util.PrioritizedValue{util.Prioritized(&markedDefinitionParser{}, 0)}, gmparser.DefaultBlockParsers()...)
	for i := range blocks {
		switch {
		case blocks[i].Value == gmparser.NewFencedCodeBlockParser():
			blocks[i].Value = &markedFenceParser{}
		case blocks[i].Value == gmparser.NewHTMLBlockParser():
			blocks[i].Value = &markedHTMLParser{}
		case reflect.TypeOf(blocks[i].Value) == reflect.TypeOf(gmparser.NewATXHeadingParser()):
			blocks[i].Value = &markedATXHeadingParser{}
		case reflect.TypeOf(blocks[i].Value) == reflect.TypeOf(gmparser.NewSetextHeadingParser()):
			blocks[i].Value = &markedSetextHeadingParser{BlockParser: gmparser.NewSetextHeadingParser()}
		}
	}
	return gmparser.NewParser(gmparser.WithBlockParsers(blocks...), gmparser.WithParagraphTransformers(gmparser.DefaultParagraphTransformers()...))
}

// Marked recognizes a multiline link definition before fenced code can interrupt its title. CommonMark paragraph splitting alone cannot preserve that token ownership. Angle destinations retain JavaScript dot's CR/LF/U+2028/U+2029 exclusions.
var mermaidReferenceDefinition = lazyregexp.New(`^ {0,3}\[((?:\\[\s\S]|[^\[\]\\])+)\]: *(?:\n[ \t]*)?([^<` + markedJSSpace + `][^` + markedJSSpace + `]*|<[^\r\n\x{2028}\x{2029}]*?>)(?:(?: +(?:\n[ \t]*)?| *\n[ \t]*)(?:"(?:\\"?|[^"\\])*"|'[^'\n]*(?:\n[^'\n]+)*\n?'|\([^()]*\)))? *(?:\n+|$)`)

func topLevelMermaidFences(markdown string) []*markedCodeBlock {
	source := []byte(markdown)
	root := mermaidBlockParser().Parse(gmtext.NewReader(source))
	var fences []*markedCodeBlock
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		code, ok := node.(*markedCodeBlock)
		if !ok || code.Info == nil || !isMermaidInfo(string(code.Info.Segment.Value(source))) {
			continue
		}
		fences = append(fences, code)
	}
	return fences
}

// createMermaidMarkdownTransformer returns a transformer that replaces top-level
// Mermaid code blocks with terminal diagrams. theme may be nil (plain art).
// While a message streams, the transformer keeps the last text it found plain (mermaidPlainText), so the frames of one streamed message read only what each delta added.
func createMermaidMarkdownTransformer(getMode func() string, theme *tui.Theme) extension.MarkdownTransformer {
	// pig additive (D92): a Piglet that strips mermaid leaves every block raw, as mermaid_off.go does.
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Mermaid) {
		return rawMermaidMarkdown
	}
	var plain mermaidPlainText
	return func(markdown string, context extension.MarkdownTransformContext) string {
		mode := getMode()
		if mode == "off" ||
			context.MessageType == extension.MarkdownMessageAssistantThinking ||
			(context.IsStreaming && mode != "streaming") {
			return markdown
		}
		if !context.IsStreaming {
			plain.forget()
		} else if plain.holds(markdown) {
			return markdown
		}
		return transformMermaidBlocks(markdown, context, theme)
	}
}

// mermaidPlainText is the last streamed text a transformer found to hold no carriage return and no "mermaid" in any letter case: transformMermaidBlocks returns such a text unchanged. A streamed message grows at its end, so a text that starts with it is plain when what follows it is, together with the len("mermaid")-1 bytes before that, where a word across the boundary would start. Only a streaming render keeps it, so a finished message does not retain a copy.
type mermaidPlainText struct {
	mu   sync.Mutex
	text string
}

// holds reports whether transformMermaidBlocks returns markdown unchanged because it holds neither a carriage return nor the word, and keeps markdown when it does.
func (p *mermaidPlainText) holds(markdown string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	rest := markdown
	if p.text != "" && strings.HasPrefix(markdown, p.text) {
		//portlint:allow utf16units the pre-check scans bytes (containsFoldMermaid is an ASCII scan), so the rest to scan starts at a byte offset
		rest = markdown[max(0, len(p.text)-len("mermaid")+1):]
	}
	if strings.IndexByte(rest, '\r') >= 0 || containsFoldMermaid(rest) {
		return false
	}
	p.text = markdown
	return true
}

func (p *mermaidPlainText) forget() {
	p.mu.Lock()
	p.text = ""
	p.mu.Unlock()
}

// transformMermaidBlocks selects direct document code tokens, as Pi mermaid.ts:72-76 does with Marked. Non-code containers retain their raw text; only the selected fence bodies reach the diagram renderer.
func transformMermaidBlocks(markdown string, context extension.MarkdownTransformContext, theme *tui.Theme) string {
	// Marked's lexer normalizes line endings before producing raw tokens.
	markdown = strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n")
	// A fence is selected only when its info string's first word equals "mermaid" ignoring case (isMermaidInfo), so text with no such
	// run holds none and the parse, repeated on every streaming frame, is skipped. No non-ASCII rune simple-folds to a letter of
	// "mermaid", so the ASCII scan is exact.
	if !containsFoldMermaid(markdown) {
		return markdown
	}
	fences := topLevelMermaidFences(markdown)
	if len(fences) == 0 {
		return markdown
	}
	var out strings.Builder
	offset := 0
	for _, fence := range fences {
		out.WriteString(markdown[offset:fence.start])
		body := strings.TrimSuffix(markdown[fence.bodyStart:fence.bodyEnd], "\n")
		if fence.fence == '`' && fence.indent > 0 {
			lines := strings.Split(body, "\n")
			for i, line := range lines {
				spaces := len(line) - len(strings.TrimLeftFunc(line, widthx.IsJSSpace))
				if len(jsstring.ToUTF16(line[:spaces])) >= fence.indent {
					lines[i] = jsstring.Slice(line, fence.indent)
				}
			}
			body = strings.Join(lines, "\n")
		}
		out.WriteString(renderMermaidToken(markdown[fence.start:fence.end], body, context, theme))
		offset = fence.end
	}
	out.WriteString(markdown[offset:])
	return out.String()
}

// mermaidFoldScanned counts the bytes containsFoldMermaid has read, the work of the pre-check on each frame.
var mermaidFoldScanned atomic.Int64

// containsFoldMermaid reports whether s holds "mermaid" in any letter case.
func containsFoldMermaid(s string) bool {
	mermaidFoldScanned.Add(int64(len(s)))
	const word = "mermaid"
	for i := 0; i+len(word) <= len(s); i++ {
		if (s[i] == 'm' || s[i] == 'M') && strings.EqualFold(s[i:i+len(word)], word) {
			return true
		}
	}
	return false
}

// isMermaidFenceClose applies Marked's closing rule: up to three spaces, the opener's matching run, optional mixed backtick/tilde suffixes, then spaces only.
func isMermaidFenceClose(line string, fenceLength int, fence byte) bool {
	unindented := strings.TrimLeft(line, " ")
	if len(line)-len(unindented) > 3 {
		return false
	}
	rest := strings.TrimLeft(unindented, string(fence))
	if len(unindented)-len(rest) < fenceLength {
		return false
	}
	return strings.Trim(strings.TrimLeft(rest, "`~"), " ") == ""
}

// isMermaidInfo reports whether a fence info string names mermaid (first word,
// lowercased), mirroring upstream isMermaid.
func isMermaidInfo(info string) bool {
	fields := strings.FieldsFunc(info, widthx.IsJSSpace)
	if len(fields) == 0 {
		return false
	}
	return strings.EqualFold(fields[0], "mermaid")
}

// renderMermaidToken preserves the raw source when the natural layout is unsupported or wider than the available area, including during streaming.
func renderMermaidToken(raw, text string, context extension.MarkdownTransformContext, theme *tui.Theme) string {
	art, ok := mermaid.Render(text)
	if !ok || art.Width > context.AvailableWidth {
		return raw
	}
	if !context.IsStreaming && len(art.Warnings) > 0 {
		suffix := ""
		if len(art.Warnings) > 1 {
			suffix = " (+" + strconv.Itoa(len(art.Warnings)-1) + " more)"
		}
		return raw + mermaidHint(art.Warnings[0]+suffix, context, theme)
	}
	lines := art.Plain
	if theme != nil {
		lines = themedLines(art, theme)
	}
	wrapped := make([]string, len(lines))
	for i, l := range lines {
		wrapped[i] = codeSpan(l)
	}
	return strings.Join(wrapped, "  \n") + "\n"
}

// mermaidHint appends the partial-parse warning after the raw code block once streaming finishes.
func mermaidHint(reason string, context extension.MarkdownTransformContext, theme *tui.Theme) string {
	if context.IsStreaming || reason == "" {
		return ""
	}
	line := "Mermaid diagram not rendered: " + reason
	if theme != nil {
		line = theme.Fg("warning", line)
	}
	return "\n" + codeSpan(line) + "  \n"
}

// codeSpan encodes one diagram row as an inline code span so Markdown preserves
// its spacing and box-drawing glyphs. Mirrors upstream codeSpan.
func codeSpan(line string) string {
	content := line
	if content == "" {
		content = "\u00a0" // non-breaking space: an empty code span has no height
	}
	longest := 0
	for _, run := range backtickRuns(content) {
		if run > longest {
			longest = run
		}
	}
	fence := strings.Repeat("`", longest+1)
	padding := ""
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") {
		padding = " "
	}
	return fence + padding + content + padding + fence
}

// backtickRuns returns the lengths of maximal backtick runs in s.
func backtickRuns(s string) []int {
	var runs []int
	n := 0
	for _, r := range s {
		if r == '`' {
			n++
			continue
		}
		if n > 0 {
			runs = append(runs, n)
			n = 0
		}
	}
	if n > 0 {
		runs = append(runs, n)
	}
	return runs
}

// styleSpan applies the theme color associated with each Mermaid semantic span.
func styleSpan(span mermaid.Span, theme *tui.Theme) string {
	switch span.Cls {
	case mermaid.ClsBorder:
		return theme.Fg("borderMuted", span.Text)
	case mermaid.ClsText:
		return theme.Fg("text", span.Text)
	case mermaid.ClsEdge:
		return theme.Fg("accent", span.Text)
	case mermaid.ClsEdgeLabel:
		return theme.Fg("muted", span.Text)
	case mermaid.ClsTitle:
		return theme.Fg("accent", "\x1b[1m"+span.Text+tui.SGRBoldDimReset)
	}
	return span.Text
}

func themedLines(art mermaid.Art, theme *tui.Theme) []string {
	out := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b strings.Builder
		for _, span := range row {
			b.WriteString(styleSpan(span, theme))
		}
		out[i] = b.String()
	}
	return out
}
