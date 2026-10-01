package tui

// BranchSummaryComponent renders a collapsible branch-summary marker.
// It preserves Pi's horizontal and vertical Box padding.

// customMsgLabelBranchFg is the fg color for the [branch] label: the active
// theme's "customMessageLabel" token, as for [compaction] (Pi
// branch-summary-message.ts theme.fg("customMessageLabel", ...)).
func customMsgLabelBranchFg() string { return customMsgLabelFg() }

// BranchSummaryComponent renders a branch-summary boundary marker in the chat
// transcript. Replaces the single-row BranchSummaryChip.
//
// Upstream: components/branch-summary-message.ts (BranchSummaryMessageComponent).
type BranchSummaryComponent struct {
	invalidatable
	summary  string
	expanded bool
}

// NewBranchSummaryComponent creates a branch summary component.
// summary is the LLM-generated markdown text summarising the abandoned branch.
func NewBranchSummaryComponent(summary string) *BranchSummaryComponent {
	return &BranchSummaryComponent{summary: summary}
}

// SetExpanded opens or collapses the component body. InteractiveMode calls it
// from the global Ctrl+O toggle.
func (c *BranchSummaryComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.Invalidate()
}

// HandleMouse toggles the summary on a left click inside the box content, excluding its one-cell padding.
// Ports packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts:59.
func (c *BranchSummaryComponent) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	if event.Type != MouseClick || event.Button != MouseButtonLeft || event.X < 1 || event.X-1 >= max(1, event.Width-2) || event.Y < 1 || event.Y >= event.Height-1 {
		return nil
	}
	c.SetExpanded(!c.expanded)
	return &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true}}
}

// Render returns the lines for this component at the given terminal width.
// All column measurements are delegated to paintBgWith (which uses
// lineDisplayWidth / runewidth.StringWidth internally).
func (c *BranchSummaryComponent) Render(width int) []string {
	if width < 4 {
		width = 4
	}

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

	// Label row: " [branch]" with bold + label fg color.
	// Mirrors upstream: theme.fg("customMessageLabel", `\x1b[1m[branch]\x1b[22m`)
	labelStyled := customMsgLabelBranchFg() + bold + "[branch]" + boldEnd + reset
	out = append(out, paintBgWith(customMsgBgOpen, " "+labelStyled, width))

	// Structural blank row: mirrors upstream Spacer(1) child between label and body.
	out = append(out, paintBgWith(customMsgBgOpen, "", width))

	if c.expanded {
		// Expanded: render "**Branch Summary**\n\n<summary>" as markdown.
		// Mirrors upstream: new Markdown(header + message.summary, 0, 0, markdownTheme, ...)
		header := "**Branch Summary**\n\n"
		md := NewMarkdown(header + c.summary)
		contentWidth := max(width-2, 1)
		for _, line := range md.Render(contentWidth) {
			out = append(out, paintBgWith(customMsgBgOpen, " "+line, width))
		}
	} else {
		// Collapsed: single summary line with Ctrl+O hint.
		// Mirrors upstream:
		//   theme.fg("customMessageText", "Branch summary (") +
		//   theme.fg("dim", keyText("app.tools.expand")) +
		//   theme.fg("customMessageText", " to expand)")
		// customMessageText is "" in dark.json: no extra fg color.
		// keyText("app.tools.expand") → lowercase "ctrl+o" (core/keybindings.ts:85;
		// keyText does not capitalize: only keyDisplayText does).
		body := "Branch summary (" + dim + "ctrl+o" + reset + " to expand)"
		out = append(out, paintBgWith(customMsgBgOpen, " "+body, width))
	}

	// Bottom padding row: mirrors upstream Box paddingY=1.
	out = append(out, paintBgWith(customMsgBgOpen, "", width))

	return out
}
