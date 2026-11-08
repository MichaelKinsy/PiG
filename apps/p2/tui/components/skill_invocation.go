package components

// Copied from coding-agent interactive/components/skill-invocation-message.ts and core/agent-session.ts (`parseSkillBlock`): a user message that
// invokes a skill (`/skill:name`, p2-tui-port.md 10.8) shows the skill as a collapsed block in the custom-message colors; the user's own text
// follows as a normal user message.
//
// pi: apps/p2/src/tui/components/skill-invocation.ts

import (
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ParsedSkillBlock is a skill block: the skill, where it is, its text, and the user's own text after it.
type ParsedSkillBlock struct {
	Name     string
	Location string
	Content  string
	// UserMessage is "" when the user wrote nothing besides the block.
	UserMessage string
}

var skillBlock = regexp.MustCompile(`(?s)^<skill name="([^"]+)" location="([^"]+)">\n(.*?)\n</skill>(?:\n\n(.+))?$`)

// ParseSkillBlock is pi's `parseSkillBlock`: the whole text is a skill block, optionally followed by the user's text. nil when it is not.
func ParseSkillBlock(text string) *ParsedSkillBlock {
	match := skillBlock.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	return &ParsedSkillBlock{Name: match[1], Location: match[2], Content: match[3], UserMessage: strings.TrimFunc(match[4], widthx.IsJSSpace)}
}

// SkillInvocationComponent is a skill block in the custom-message colors.
type SkillInvocationComponent struct {
	*tui.Box
	expanded bool
	block    ParsedSkillBlock
	ctx      render.RenderContext
}

// NewSkillInvocationComponent is the constructor; outputPad is the horizontal padding (default 1).
func NewSkillInvocationComponent(block ParsedSkillBlock, ctx render.RenderContext, outputPad int) *SkillInvocationComponent {
	box := tui.NewBox()
	box.PaddingX, box.PaddingY = outputPad, 1
	box.BgFn = func(text string) string { return ctx.Theme().Current().Bg("customMessageBg", text) }
	c := &SkillInvocationComponent{Box: box, block: block, ctx: ctx}
	c.updateDisplay()
	return c
}

// SetExpanded shows the skill's text.
func (c *SkillInvocationComponent) SetExpanded(expanded bool) {
	if expanded == c.expanded {
		return
	}
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate drops the caches and rebuilds the display (the theme may have changed).
func (c *SkillInvocationComponent) Invalidate() {
	c.Box.Invalidate()
	c.updateDisplay()
}

func (c *SkillInvocationComponent) updateDisplay() {
	c.Box.Clear()
	theme := c.ctx.Theme().Current()
	content := tui.NewContainer()
	label := theme.Fg("customMessageLabel", theme.Bold("[skill]"))
	if c.expanded {
		content.AddChild(tui.NewPaddedText(label, 0, 0, nil))
		markdownTheme := c.ctx.Theme().Markdown
		content.AddChild(tui.NewMarkdownWithOptions("**"+render.SafeText(c.block.Name)+"**\n\n"+render.SafeText(c.block.Content), 0, 0, &markdownTheme,
			&tui.DefaultTextStyle{Color: func(text string) string { return theme.Fg("customMessageText", text) }}, nil))
	} else {
		content.AddChild(tui.NewPaddedText(label+" "+theme.Fg("customMessageText", render.LineText(c.block.Name))+theme.Fg("dim", " ("+c.ctx.KeyText("app.tools.expand")+" to expand)"), 0, 0, nil))
	}
	c.Box.AddChild(content)
}
