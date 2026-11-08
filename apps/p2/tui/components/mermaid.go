// Copied from coding-agent interactive/components/mermaid.ts (p2-tui-port.md section 7, P12): top-level Mermaid code blocks become Unicode
// diagrams (pure, no I/O, so it runs in the permission-dropped worker). The mode is the visitor's `markdown.mermaid` setting (pi's default
// "streaming") and the theme the instance theme, both from the transform context instead of pi's settings manager and global theme.
//
// pi: apps/p2/src/tui/components/mermaid.ts
package components

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/mermaid"
)

var backtickRun = regexp.MustCompile("`+")

// mermaidCodeSpan encodes each diagram row as inline code so Markdown preserves its spacing and box-drawing characters. A non-breaking space
// stands for a blank row, and the delimiter is longer than any backtick run in the content.
func mermaidCodeSpan(line string) string {
	content := line
	if content == "" {
		content = "\u00a0"
	}
	longest := 0
	for _, run := range backtickRun.FindAllString(content, -1) {
		longest = max(longest, len(run))
	}
	fence := strings.Repeat("`", longest+1)
	padding := ""
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") {
		padding = " "
	}
	return fence + padding + content + padding + fence
}

func styleMermaidSpan(span mermaid.Span, t *theme.Theme) string {
	switch span.Cls {
	case mermaid.ClsBorder:
		return t.Fg("borderMuted", span.Text)
	case mermaid.ClsText:
		return t.Fg("text", span.Text)
	case mermaid.ClsEdge:
		return t.Fg("accent", span.Text)
	case mermaid.ClsEdgeLabel:
		return t.Fg("muted", span.Text)
	case mermaid.ClsTitle:
		return t.Fg("accent", t.Bold(span.Text))
	}
	return span.Text
}

func mermaidThemedLines(art mermaid.Art, t *theme.Theme) []string {
	lines := make([]string, len(art.Styled))
	for i, row := range art.Styled {
		var b strings.Builder
		for _, span := range row {
			b.WriteString(styleMermaidSpan(span, t))
		}
		lines[i] = b.String()
	}
	return lines
}

// mermaidMode is pi's default when the setting is absent.
func mermaidMode(context render.MarkdownTransformContext) string {
	if context.Settings != nil && context.Settings.Markdown != nil && context.Settings.Markdown.Mermaid != nil {
		if mode := *context.Settings.Markdown.Mermaid; mode == "off" || mode == "final" {
			return mode
		}
	}
	return "streaming"
}

// MermaidMarkdownTransformer replaces top-level Mermaid code blocks with Unicode terminal diagrams.
func MermaidMarkdownTransformer(markdown string, context render.MarkdownTransformContext) string {
	mode := mermaidMode(context)
	if mode == "off" || context.MessageType == render.MessageAssistantThinking || (context.IsStreaming && mode != "streaming") {
		return markdown
	}
	return codingagent.ReplaceMermaidFences(markdown, func(raw, text string) string {
		art, ok := mermaid.Render(text)
		if !ok || art.Width > context.AvailableWidth {
			return raw
		}
		if !context.IsStreaming && len(art.Warnings) > 0 {
			suffix := ""
			if len(art.Warnings) > 1 {
				suffix = " (+" + strconv.Itoa(len(art.Warnings)-1) + " more)"
			}
			warning := "Mermaid diagram not rendered: " + art.Warnings[0] + suffix
			return raw + "\n" + mermaidCodeSpan(context.Theme.Fg("warning", warning)) + "  \n"
		}
		lines := mermaidThemedLines(art, context.Theme)
		for i, line := range lines {
			lines[i] = mermaidCodeSpan(line)
		}
		// Markdown hard breaks keep every diagram row on its own line.
		return strings.Join(lines, "  \n") + "\n"
	})
}
