package tui

import "github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"

// BranchSummaryMessageComponent renders a branch-summary boundary marker in the chat transcript. Like upstream's
// `extends Box`, it embeds the Box (padding 1, 1 and the customMessageBg background), so the Box members are inherited;
// each display update clears the Box and adds one mouse region.
//
// upstream: packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts
type BranchSummaryMessageComponent struct {
	*Box
	summary       string
	expanded      bool
	markdownTheme *MarkdownTheme
	surface       markdownSurface
}

// BranchSummaryMessage is the part of Pi's BranchSummaryMessage (coding-agent core/messages.ts:55) that
// [BranchSummaryMessageComponent] renders: the LLM-generated markdown summarising the abandoned branch.
type BranchSummaryMessage struct {
	Summary string
}

// NewBranchSummaryMessageComponent is Pi's constructor(message, markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = [],
// messageId?, timestamp?) (branch-summary-message.ts:20). The expanded body renders the summary with markdownTheme (nil selects the active theme's
// markdown theme) and the Box pads its rows by outputPad columns. markdownTransformers run in order on the summary as the display-only transform
// createMarkdownTransform("summary", false, markdownTransformers, messageId, timestamp); messageID and timestamp are the persisted entry's, empty for a live message.
func NewBranchSummaryMessageComponent(message BranchSummaryMessage, markdownTheme *MarkdownTheme, outputPad int, markdownTransformers []markdowntransform.MarkdownTransformer, messageID, timestamp string) *BranchSummaryMessageComponent {
	return NewBranchSummaryMessageComponentWithSurface(message, markdownTheme, outputPad, summarySurface(markdownTransformers, messageID, timestamp))
}

// NewBranchSummaryMessageComponentWithSurface is [NewBranchSummaryMessageComponent] with a [MarkdownSurface] in place of the transformers, messageId and timestamp (pig additive, D19: the surface can run its chain off the render loop).
func NewBranchSummaryMessageComponentWithSurface(message BranchSummaryMessage, markdownTheme *MarkdownTheme, outputPad int, surface MarkdownSurface) *BranchSummaryMessageComponent {
	component := &BranchSummaryMessageComponent{
		Box:           NewPaddedBox(outputPad, 1, func(text string) string { return ActiveTheme().Bg("customMessageBg", text) }),
		summary:       message.Summary,
		markdownTheme: markdownTheme,
	}
	component.surface.install(surface, &component.invalidatable)
	component.updateDisplay()
	return component
}

// SetExpanded opens or collapses the component body. InteractiveMode calls it from the global Ctrl+O toggle.
func (c *BranchSummaryMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate invalidates the Box and rebuilds the display, which bakes theme colors into its text.
func (c *BranchSummaryMessageComponent) Invalidate() {
	c.Box.Invalidate()
	c.updateDisplay()
}

func (c *BranchSummaryMessageComponent) updateDisplay() {
	c.Clear()
	theme := ActiveTheme()
	content := NewContainer()
	content.Add(NewText(theme.Fg("customMessageLabel", "\x1b[1m[branch]\x1b[22m")))
	content.Add(NewSpacer(1))
	if c.expanded {
		content.Add(c.surface.markdown("**Branch Summary**\n\n", c.summary, c.markdownTheme, &DefaultTextStyle{
			Color: func(text string) string { return ActiveTheme().Fg("customMessageText", text) },
		}))
	} else {
		content.Add(NewText(theme.Fg("customMessageText", "Branch summary (") +
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

// SetOutputPad is Pi's setOutputPad(outputPad): the horizontal padding of the Box is the outputPad setting.
func (c *BranchSummaryMessageComponent) SetOutputPad(outputPad int) {
	c.SetPaddingX(outputPad)
}

// Dispose releases the expanded body's pending Markdown generation.
func (c *BranchSummaryMessageComponent) Dispose() { c.surface.Dispose() }
