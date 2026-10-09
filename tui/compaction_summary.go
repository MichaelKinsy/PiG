package tui

import (
	"strconv"
	"strings"
)

// customMsgLabelFg is the foreground color for the [compaction] label.
// It is the active theme's customMessageLabel token.
// Fg colors do not need the delta-from-cardBg adjustment (only bg tints do).
func customMsgLabelFg() string { return ActiveTheme().CustomMessageLabel }

// CompactionSummaryMessageComponent renders a collapsible compaction marker. Like upstream's `extends Box`, it embeds the
// Box (padding 1, 1 and the customMessageBg background), so the Box members are inherited; each display update clears the
// Box and adds one mouse region.
//
// upstream: packages/coding-agent/src/modes/interactive/components/compaction-summary-message.ts
type CompactionSummaryMessageComponent struct {
	*Box
	summary       string
	tokensBefore  int
	expanded      bool
	markdownTheme *MarkdownTheme
}

// CompactionSummaryMessage is the part of Pi's CompactionSummaryMessage (coding-agent core/messages.ts:62) that
// [CompactionSummaryMessageComponent] renders: the summary markdown and the context token count before compaction.
type CompactionSummaryMessage struct {
	Summary      string
	TokensBefore int
}

// NewCompactionSummaryMessageComponent is Pi's constructor(message, markdownTheme = getMarkdownTheme(), outputPad = 1)
// (compaction-summary-message.ts:15). The expanded body renders the summary with markdownTheme; nil selects the active
// theme's markdown theme. outputPad is the horizontal padding; absent it is 1.
func NewCompactionSummaryMessageComponent(message CompactionSummaryMessage, markdownTheme *MarkdownTheme, outputPad ...int) *CompactionSummaryMessageComponent {
	padX := 1
	if len(outputPad) > 0 {
		padX = outputPad[0]
	}
	component := &CompactionSummaryMessageComponent{
		Box:           NewPaddedBox(padX, 1, func(text string) string { return ActiveTheme().Bg("customMessageBg", text) }),
		summary:       message.Summary,
		tokensBefore:  message.TokensBefore,
		markdownTheme: markdownTheme,
	}
	component.updateDisplay()
	return component
}

// SetExpanded opens or collapses the component body. InteractiveMode calls it from the global Ctrl+O toggle.
func (c *CompactionSummaryMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate invalidates the Box and rebuilds the display, which bakes theme colors into its text.
func (c *CompactionSummaryMessageComponent) Invalidate() {
	c.Box.Invalidate()
	c.updateDisplay()
}

func (c *CompactionSummaryMessageComponent) updateDisplay() {
	c.Clear()
	theme := ActiveTheme()
	content := NewContainer()
	tokenStr := formatThousands(c.tokensBefore)
	content.Add(NewText(theme.Fg("customMessageLabel", "\x1b[1m[compaction]\x1b[22m")))
	content.Add(NewSpacer(1))
	if c.expanded {
		header := "**Compacted from " + tokenStr + " tokens**\n\n"
		content.Add(NewMarkdownWithOptions(header+c.summary, 0, 0, c.markdownTheme, &DefaultTextStyle{
			Color: func(text string) string { return ActiveTheme().Fg("customMessageText", text) },
		}, nil))
	} else {
		content.Add(NewText(theme.Fg("customMessageText", "Compacted from "+tokenStr+" tokens (") +
			theme.Fg("dim", AppKeyText("app.tools.expand", "ctrl+o")) +
			theme.Fg("customMessageText", " to expand)")))
	}
	c.AddChild(NewMouseRegion(content, func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseClick || event.Button != MouseButtonLeft {
			return nil
		}
		c.SetExpanded(!c.expanded)
		return &TuiMouseEventResult{Handled: true}
	}))
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

// SetOutputPad is Pi's setOutputPad(outputPad): the horizontal padding of the Box is the outputPad setting.
func (c *CompactionSummaryMessageComponent) SetOutputPad(outputPad int) {
	c.SetPaddingX(outputPad)
}
