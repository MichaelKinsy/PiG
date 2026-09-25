package codingagent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/mermaid"
	"github.com/MichaelKinsy/PiG/tui"
)

// Mermaid markdown transformer, ported from upstream
// modes/interactive/components/mermaid.ts. Replaces top-level ```mermaid code
// blocks with Unicode box-drawing diagrams (internal/mermaid), gated by the
// MermaidRenderingMode and the message/streaming context.

// mermaidFenceOpen matches a fenced-code opening line (up to 3 leading spaces,
// 3+ backticks, optional info string), mirroring marked's fenced-code tokenizer.
var mermaidFenceOpen = regexp.MustCompile("^( {0,3})(`{3,})(.*)$")

// createMermaidMarkdownTransformer returns a transformer that replaces top-level
// Mermaid code blocks with terminal diagrams. theme may be nil (plain art).
func createMermaidMarkdownTransformer(getMode func() string, theme *tui.Theme) extension.MarkdownTransformer {
	return func(markdown string, context extension.MarkdownTransformContext) string {
		mode := getMode()
		if mode == "off" ||
			context.MessageType == extension.MarkdownMessageAssistantThinking ||
			(context.IsStreaming && mode != "streaming") {
			return markdown
		}
		return transformMermaidBlocks(markdown, context, theme)
	}
}

// transformMermaidBlocks scans markdown for top-level ```mermaid fenced blocks
// and replaces each with its rendered diagram, leaving all other bytes intact
// (marked's lexer→map→join round-trips non-code tokens to their raw source).
func transformMermaidBlocks(markdown string, context extension.MarkdownTransformContext, theme *tui.Theme) string {
	lines := strings.SplitAfter(markdown, "\n") // keep trailing newlines
	var out strings.Builder
	for i := 0; i < len(lines); {
		open := mermaidFenceOpen.FindStringSubmatch(strings.TrimSuffix(lines[i], "\n"))
		if open == nil {
			out.WriteString(lines[i])
			i++
			continue
		}
		fence := open[2]
		info := open[3]
		if !isMermaidInfo(info) {
			out.WriteString(lines[i])
			i++
			continue
		}
		// Collect the block until a closing fence of >= len(fence) backticks.
		closeRE := regexp.MustCompile("^ {0,3}`{" + strconv.Itoa(len(fence)) + ",}[ \\t]*$")
		var raw strings.Builder
		raw.WriteString(lines[i])
		var body []string
		j := i + 1
		closed := false
		for ; j < len(lines); j++ {
			line := strings.TrimSuffix(lines[j], "\n")
			raw.WriteString(lines[j])
			if closeRE.MatchString(line) {
				j++
				closed = true
				break
			}
			body = append(body, line)
		}
		// marked includes the newline after the closing fence in token.raw only
		// when a non-blank line follows immediately (back-to-back blocks); before
		// a blank-line boundary or EOF it belongs to the following space token.
		// Strip it there and re-emit so surrounding blank lines survive exactly.
		rawToken := raw.String()
		trailingNL := ""
		nextBlank := j >= len(lines) || strings.TrimSpace(strings.TrimSuffix(lines[j], "\n")) == ""
		if closed && nextBlank && strings.HasSuffix(rawToken, "\n") {
			rawToken = rawToken[:len(rawToken)-1]
			trailingNL = "\n"
		}
		text := strings.Join(body, "\n")
		out.WriteString(renderMermaidToken(rawToken, text, context, theme))
		out.WriteString(trailingNL)
		i = j
	}
	return out.String()
}

// isMermaidInfo reports whether a fence info string names mermaid (first word,
// lowercased), mirroring upstream isMermaid.
func isMermaidInfo(info string) bool {
	fields := strings.Fields(info)
	if len(fields) == 0 {
		return false
	}
	return strings.ToLower(fields[0]) == "mermaid"
}

// renderMermaidToken renders one Mermaid token or returns its source.
// pig divergence (D50): final failures include a display-only cause.
func renderMermaidToken(raw, text string, context extension.MarkdownTransformContext, theme *tui.Theme) string {
	// pig divergence (D58): narrow labels before rejecting an over-wide diagram.
	art, ok := mermaid.RenderWithin(text, context.AvailableWidth)
	if !ok {
		return raw + mermaidHint(unrenderableReason(text), context, theme)
	}
	if art.Width > context.AvailableWidth {
		return raw + mermaidHint(oversizeReason(art.Width, context.AvailableWidth), context, theme)
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

// mermaidHint formats one "not rendered" line to append after the raw block, or
// "" while streaming. Mirrors the shape upstream already uses for dropped
// statements so all unrendered outcomes read identically.
func mermaidHint(reason string, context extension.MarkdownTransformContext, theme *tui.Theme) string {
	if context.IsStreaming || reason == "" {
		return ""
	}
	line := "Mermaid diagram not rendered: " + reason
	if theme != nil {
		line = theme.FgText("warning", line)
	}
	return "\n" + codeSpan(line) + "  \n"
}

// unrenderableReason names why the grammar rejected a diagram, in terms an
// author can act on. A semicolon inside a statement is called out because it is
// mermaid's statement separator: `A->>B: a;b` parses as a message followed by a
// bogus statement `b`, which fails the whole diagram, while a trailing `;` is
// fine.
func unrenderableReason(text string) string {
	kind := mermaid.DiagramKind(text)
	if kind == "" {
		return "unrecognized diagram type (flowchart, state, class, er, sequence)"
	}
	if hasInlineSemicolon(text) {
		return "could not parse " + kind + " diagram; a ';' inside a statement splits it, so quote the text"
	}
	return "could not parse " + kind + " diagram"
}

// oversizeReason names an area overflow with both measurements, so the author
// can shorten the diagram instead of re-reading the grammar. Returns "" when the
// area is unmeasured (width 0 during early layout), where no honest number
// exists to report.
func oversizeReason(artWidth, availableWidth int) string {
	if availableWidth <= 0 {
		return ""
	}
	return "diagram is " + strconv.Itoa(artWidth) + " columns wide, area is " +
		strconv.Itoa(availableWidth) + "; shorten the longest label or split the diagram"
}

// hasInlineSemicolon reports whether any line carries a semicolon before its
// end, i.e. used as a separator rather than a terminator.
func hasInlineSemicolon(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if i := strings.IndexByte(trimmed, ';'); i >= 0 && i < len(trimmed)-1 {
			return true
		}
	}
	return false
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

// styleSpan maps a diagram span's semantic class to pig's theme, mirroring
// upstream styleSpan (theme colours differ from pi's, so themed output is not
// byte-comparable across the two: only the span structure is parity-relevant).
func styleSpan(span mermaid.Span, theme *tui.Theme) string {
	switch span.Cls {
	case mermaid.ClsBorder:
		return theme.FgText("borderMuted", span.Text)
	case mermaid.ClsText:
		return theme.FgText("text", span.Text)
	case mermaid.ClsEdge:
		return theme.FgText("accent", span.Text)
	case mermaid.ClsEdgeLabel:
		return theme.FgText("muted", span.Text)
	case mermaid.ClsTitle:
		return theme.FgText("accent", "\x1b[1m"+span.Text+tui.SGRBoldDimReset)
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
