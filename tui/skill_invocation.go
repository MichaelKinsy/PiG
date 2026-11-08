package tui

// skill_invocation.go: skill invocation display component.
//
// Ports upstream skill-invocation-message.ts: a Box that holds a mouse region over the collapsed line or the expanded
// header and markdown body.

// ParsedSkillBlock holds the parsed skill invocation data.
// Mirrors upstream's ParsedSkillBlock.
type ParsedSkillBlock struct {
	Name    string
	Content string
}

// SkillInvocationMessageComponent renders a skill invocation message. Like upstream's `extends Box`, it embeds the
// Box (padding 1, 1 and the customMessageBg background), so the Box members are inherited; each display update clears
// the Box and adds one mouse region.
type SkillInvocationMessageComponent struct {
	*Box
	skillBlock    ParsedSkillBlock
	expanded      bool
	markdownTheme *MarkdownTheme
	surface       markdownSurface
}

// NewSkillInvocationMessageComponent creates a skill invocation component (constructor(skillBlock, markdownTheme = getMarkdownTheme(), outputPad = 1), skill-invocation-message.ts:16). A nil markdownTheme is the default markdown theme; outputPad is the horizontal padding of the Box.
func NewSkillInvocationMessageComponent(block ParsedSkillBlock, markdownTheme *MarkdownTheme, outputPad int) *SkillInvocationMessageComponent {
	component := &SkillInvocationMessageComponent{
		Box:           NewPaddedBox(outputPad, 1, func(text string) string { return ActiveTheme().Bg("customMessageBg", text) }),
		skillBlock:    block,
		markdownTheme: markdownTheme,
	}
	component.updateDisplay()
	return component
}

// SetExpanded toggles expanded/collapsed rendering.
func (s *SkillInvocationMessageComponent) SetExpanded(expanded bool) {
	s.expanded = expanded
	s.updateDisplay()
}

// Invalidate invalidates the Box and rebuilds the display, which bakes theme colors into its text.
func (s *SkillInvocationMessageComponent) Invalidate() {
	s.Box.Invalidate()
	s.updateDisplay()
}

func (s *SkillInvocationMessageComponent) updateDisplay() {
	s.Clear()
	theme := ActiveTheme()
	content := NewContainer()
	if s.expanded {
		label := theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m")
		content.Add(NewText(label))
		header := "**" + s.skillBlock.Name + "**\n\n"
		content.Add(s.surface.markdown(header, s.skillBlock.Content, s.markdownTheme, &DefaultTextStyle{
			Color: func(text string) string { return ActiveTheme().Fg("customMessageText", text) },
		}))
	} else {
		line := theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m ") +
			theme.Fg("customMessageText", s.skillBlock.Name) +
			theme.Fg("dim", " ("+AppKeyText("app.tools.expand", "ctrl+o")+" to expand)")
		content.Add(NewText(line))
	}
	s.AddChild(NewMouseRegion(content, func(event TuiMouseEvent) *TuiMouseEventResult {
		if event.Type != MouseClick || event.Button != MouseButtonLeft {
			return nil
		}
		s.SetExpanded(!s.expanded)
		return &TuiMouseEventResult{Handled: true}
	}))
}

// SetOutputPad is Pi's setOutputPad(outputPad): the horizontal padding of the Box is the outputPad setting.
func (c *SkillInvocationMessageComponent) SetOutputPad(outputPad int) {
	c.SetPaddingX(outputPad)
}

// SetMarkdownTransform installs the display-only Markdown transform of the expanded body, which sees the skill content without the header, and the state it reads for the Markdown render cache key. It is Pi's markdownTransformers, messageId and timestamp of the skill block component; a nil transform shows the Markdown as it is.
func (c *SkillInvocationMessageComponent) SetMarkdownTransform(transform func(markdown string, availableWidth int) string, state func() string) {
	c.surface.transform, c.surface.state = transform, state
	c.updateDisplay()
}
