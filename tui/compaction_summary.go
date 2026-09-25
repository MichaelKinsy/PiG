package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// customMsgLabelFg is the foreground color for the [compaction] label.
// Mirrors upstream theme/dark.json "customMessageLabel": "#9575cd" exactly.
// Fg colors do not need the delta-from-cardBg adjustment (only bg tints do).
func customMsgLabelFg() string { return ThemeHexFg("#9575cd") }

// CompactionSummaryComponent renders a collapsible compaction marker.
// It preserves Pi's horizontal and vertical Box padding.
type CompactionSummaryComponent struct {
	invalidatable
	summary      string
	tokensBefore int
	expanded     bool
}

// NewCompactionSummaryComponent creates a compaction summary component.
// summary is the markdown text of the compaction context; tokensBefore is the
// LLM token count before compaction (shown in the header).
func NewCompactionSummaryComponent(summary string, tokensBefore int) *CompactionSummaryComponent {
	return &CompactionSummaryComponent{
		summary:      summary,
		tokensBefore: tokensBefore,
	}
}

// SetExpanded opens or collapses the component body. InteractiveMode calls it
// from the global Ctrl+O toggle.
func (c *CompactionSummaryComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.Invalidate()
}

// Render returns the lines for this component at the given terminal width.
// All column measurements are delegated to paintBgWith (which uses
// lineDisplayWidth / runewidth.StringWidth internally).
func (c *CompactionSummaryComponent) Render(width int) []string {
	if width < 4 {
		width = 4
	}
	tokenStr := formatThousands(c.tokensBefore)

	const (
		bold    = "\x1b[1m"
		boldEnd = "\x1b[22m"
		dim     = "\x1b[2m"
		reset   = "\x1b[0m"
	)

	customMsgBgOpen := ActiveTheme().CustomMessageBg
	var out []string

	// Top padding row: mirrors upstream Box(paddingX=1, paddingY=1).
	out = append(out, paintBgWith(customMsgBgOpen, "", width))

	// Label row: " [compaction]" with bold + label fg color.
	// Mirrors upstream: theme.fg("customMessageLabel", `\x1b[1m[compaction]\x1b[22m`)
	labelStyled := customMsgLabelFg() + bold + "[compaction]" + boldEnd + reset
	out = append(out, paintBgWith(customMsgBgOpen, " "+labelStyled, width))

	// Structural blank row: mirrors upstream Spacer(1) child between label
	// and body.
	out = append(out, paintBgWith(customMsgBgOpen, "", width))

	if c.expanded {
		// Expanded: render markdown header + summary body.
		// Upstream: new Markdown(header + summary, 0, 0, markdownTheme, ...)
		header := fmt.Sprintf("**Compacted from %s tokens**\n\n", tokenStr)
		md := NewMarkdown(header + c.summary)
		contentWidth := max(width-2, 1)
		for _, line := range md.Render(contentWidth) {
			out = append(out, paintBgWith(customMsgBgOpen, " "+line, width))
		}
	} else {
		// Collapsed: single summary line with Ctrl+O hint.
		// Upstream: theme.fg("customMessageText", "Compacted from N tokens (") +
		//           theme.fg("dim", keyText("app.tools.expand")) +
		//           theme.fg("customMessageText", " to expand)")
		// customMessageText is "" in dark.json: no extra fg color.
		// keyText("app.tools.expand") resolves to lowercase "ctrl+o" (default key,
		// core/keybindings.ts:85: "app.tools.expand": { defaultKeys: "ctrl+o" };
		// keyText does not capitalize: only keyDisplayText does).
		body := fmt.Sprintf("Compacted from %s tokens (", tokenStr)
		body += dim + "ctrl+o" + reset + " to expand)"
		out = append(out, paintBgWith(customMsgBgOpen, " "+body, width))
	}

	// Bottom padding row: mirrors upstream Box paddingY=1.
	out = append(out, paintBgWith(customMsgBgOpen, "", width))

	return out
}

// formatThousands formats n with comma thousands separators, e.g. 87432 → "87,432".
// Handles non-negative integers (token counts are always ≥ 0).
func formatThousands(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + (len(s)-1)/3)
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}
