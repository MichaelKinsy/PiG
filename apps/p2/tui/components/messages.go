package components

// Copied from coding-agent interactive/components/{user-message,assistant-message,compaction-summary-message}.ts with the instance theme
// (section 5.4) and sanitized server text. p2 additions to the user message (section 1.3): author label with the guest "unverified name" note,
// peer attribution, reply quote, plugin note, files and image count.
//
// pi: apps/p2/src/tui/components/messages.ts

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const (
	osc133ZoneStart = "\x1b]133;A\x07"
	osc133ZoneEnd   = "\x1b]133;B\x07"
	osc133ZoneFinal = "\x1b]133;C\x07"
)

// MessageContext is what message components need besides the render context.
type MessageContext interface {
	render.RenderContext
	MarkdownTransformers() []render.MarkdownTransformer
	// Settings are the visitor's settings (Markdown transformers read e.g. `markdown.mermaid`); nil when none.
	Settings() *prefs.TuiSettings
	// OutputPad is the horizontal padding of output; implementations return 1 when the visitor set none.
	OutputPad() int
}

func transformSource(ctx MessageContext) render.TransformSource {
	return render.TransformSource{Theme: ctx.Theme(), Settings: ctx.Settings}
}

// zone adds OSC 133 prompt zones: pi-tui uses them for prompt jumps and strips them from screen lines.
func zone(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := append([]string(nil), lines...)
	out[0] = osc133ZoneStart + out[0]
	out[len(out)-1] = osc133ZoneEnd + osc133ZoneFinal + out[len(out)-1]
	return out
}

// themed rebuilds baked content when the theme changed (section 5.4).
type themed struct {
	*tui.Container
	ctx          MessageContext
	themeVersion int
	rebuild      func()
}

func newThemed(ctx MessageContext) themed {
	return themed{Container: tui.NewContainer(), ctx: ctx, themeVersion: ctx.Theme().Version}
}

// Render rebuilds when the theme changed, then renders.
func (t *themed) Render(width int) []string {
	if t.themeVersion != t.ctx.Theme().Version {
		t.themeVersion = t.ctx.Theme().Version
		t.rebuild()
	}
	return t.Container.Render(width)
}

// UserMessageComponent is a person's, peer's or agent's message.
type UserMessageComponent struct {
	themed
	item transcript.UserItem
	// skill is a `/skill:` invocation (10.8): pi's collapsible skill block, then the user's own text.
	skill    *SkillInvocationComponent
	expanded bool
}

// NewUserMessageComponent is the constructor.
func NewUserMessageComponent(item transcript.UserItem, ctx MessageContext) *UserMessageComponent {
	c := &UserMessageComponent{themed: newThemed(ctx), item: item}
	c.themed.rebuild = c.rebuild
	c.rebuild()
	return c
}

// SetExpanded expands the skill block.
func (c *UserMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	if c.skill != nil {
		c.skill.SetExpanded(expanded)
	}
}

// Update shows another item.
func (c *UserMessageComponent) Update(item transcript.UserItem) {
	c.item = item
	c.rebuild()
}

func (c *UserMessageComponent) rebuild() {
	c.Clear()
	theme := c.ctx.Theme().Current()
	pad := c.ctx.OutputPad()
	block := ParseSkillBlock(c.item.Text)
	c.skill = nil
	item := c.item
	if block != nil {
		c.skill = NewSkillInvocationComponent(*block, c.ctx, pad)
		c.skill.SetExpanded(c.expanded)
		c.AddChild(c.skill)
		item.Text = block.UserMessage
		// Nothing of the user's own besides the skill: the block alone, as pi.
		if item.Text == "" && len(item.Files) == 0 && len(item.Images) == 0 {
			return
		}
	}
	bg := func(text string) string { return theme.Bg("userMessageBg", text) }
	markdown := func(paddingY int) tui.Component {
		paddingX := 0
		if paddingY != 0 {
			paddingX = pad
		}
		markdownTheme := c.ctx.Theme().Markdown
		return tui.NewMarkdownWithOptions(render.SafeText(item.Text), paddingX, paddingY, &markdownTheme,
			&tui.DefaultTextStyle{Color: func(content string) string { return theme.Fg("userMessageText", content) }, BgColor: bg},
			&tui.MarkdownOptions{
				PreserveOrderedListMarkers: true,
				PreserveBackslashEscapes:   true,
				Transform:                  render.CreateMarkdownTransform(render.MessageUser, false, c.ctx.MarkdownTransformers(), transformSource(c.ctx)),
			})
	}
	var head []string
	name := ""
	if item.Author != nil {
		name = item.Author.Name
	} else if item.Peer != nil {
		name = item.Peer.Title
	}
	// Guests, peers and chat-mode messages are labeled; the owner's own inputs look like pi's.
	guest := item.Author != nil && item.Author.Kind == runtime.AuthorGuest
	if name != "" && (guest || item.Peer != nil || item.Chat) {
		label := theme.Fg("accent", theme.Bold(render.LineText(name)))
		note := ""
		if guest {
			note = theme.Fg("muted", " (guest, unverified name)")
		}
		from := ""
		if item.Peer != nil {
			from = theme.Fg("muted", " · from another session")
		}
		head = append(head, label+note+from)
	}
	if item.ReplyTo != nil {
		head = append(head, theme.Fg("muted", "↪ "+render.LineText(item.ReplyTo.Name)+": "+render.LineText(item.ReplyTo.Excerpt)))
	}
	if item.Note != "" {
		head = append(head, theme.Fg("muted", render.LineText(item.Note)))
	}
	var tail []string
	for _, file := range item.Files {
		tail = append(tail, theme.Fg("muted", "[file] "+render.LineText(file)))
	}
	if len(item.Images) > 0 {
		plural := "s"
		if len(item.Images) == 1 {
			plural = ""
		}
		tail = append(tail, theme.Fg("muted", fmt.Sprintf("[%d image%s]", len(item.Images), plural)))
	}
	if len(head) == 0 && len(tail) == 0 {
		// The Markdown pads and colors its own background, as pi's.
		c.AddChild(markdown(1))
		return
	}
	box := tui.NewBox()
	box.PaddingX, box.PaddingY, box.BgFn = pad, 1, bg
	if len(head) > 0 {
		box.AddChild(tui.NewPaddedText(strings.Join(head, "\n"), 0, 0, nil))
	}
	if strings.TrimFunc(item.Text, widthx.IsJSSpace) != "" {
		box.AddChild(markdown(0))
	}
	if len(tail) > 0 {
		box.AddChild(tui.NewPaddedText(strings.Join(tail, "\n"), 0, 0, nil))
	}
	c.AddChild(box)
}

// Render renders with OSC 133 prompt zones.
func (c *UserMessageComponent) Render(width int) []string { return zone(c.themed.Render(width)) }

func hasVisibleText(content []ai.AssistantContentBlock) func(from int) bool {
	return func(from int) bool {
		for _, block := range content[from:] {
			switch typed := block.(type) {
			case ai.TextContent:
				if strings.TrimFunc(typed.Text, widthx.IsJSSpace) != "" {
					return true
				}
			case ai.ThinkingContent:
				if strings.TrimFunc(typed.Thinking, widthx.IsJSSpace) != "" {
					return true
				}
			}
		}
		return false
	}
}

// AssistantMessageComponent is an assistant answer: text, thinking, and why it ended abnormally.
type AssistantMessageComponent struct {
	themed
	contentContainer    *tui.Container
	hideThinkingBlock   bool
	hiddenThinkingLabel string
	lastMessage         *ai.AssistantMessage
	hasToolCalls        bool
	isStreaming         bool
	thinkingOverrides   map[int]bool
}

// NewAssistantMessageComponent is the constructor; message may be nil.
func NewAssistantMessageComponent(ctx MessageContext, message *ai.AssistantMessage, hideThinkingBlock bool, hiddenThinkingLabel string) *AssistantMessageComponent {
	if hiddenThinkingLabel == "" {
		hiddenThinkingLabel = "Thinking..."
	}
	c := &AssistantMessageComponent{
		themed: newThemed(ctx), contentContainer: tui.NewContainer(), hideThinkingBlock: hideThinkingBlock, hiddenThinkingLabel: hiddenThinkingLabel,
		thinkingOverrides: map[int]bool{},
	}
	c.themed.rebuild = c.rebuild
	c.AddChild(c.contentContainer)
	if message != nil {
		c.UpdateContent(message, false)
	}
	return c
}

func (c *AssistantMessageComponent) rebuild() {
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// SetHideThinkingBlock hides or shows thinking.
func (c *AssistantMessageComponent) SetHideThinkingBlock(hide bool) {
	if hide == c.hideThinkingBlock {
		return
	}
	c.hideThinkingBlock = hide
	clear(c.thinkingOverrides)
	c.rebuild()
}

// Render renders with OSC 133 prompt zones unless the answer has tool calls.
func (c *AssistantMessageComponent) Render(width int) []string {
	lines := c.themed.Render(width)
	if c.hasToolCalls {
		return lines
	}
	return zone(lines)
}

// UpdateContent shows message; isStreaming is whether it is still arriving.
func (c *AssistantMessageComponent) UpdateContent(message *ai.AssistantMessage, isStreaming bool) {
	// The content changes in the nested container, which the parent containers cache does not see: this components own flag is what makes the parent render it again.
	defer c.Invalidate()
	c.lastMessage = message
	c.isStreaming = isStreaming
	theme := c.ctx.Theme().Current()
	pad := c.ctx.OutputPad()
	markdownTheme := c.ctx.Theme().Markdown
	c.contentContainer.Clear()
	visibleFrom := hasVisibleText(message.Content)
	if visibleFrom(0) {
		c.contentContainer.AddChild(tui.NewSpacer(1))
	}
	thinkingRun := 0
	for i := 0; i < len(message.Content); i++ {
		switch content := message.Content[i].(type) {
		case ai.TextContent:
			if text := strings.TrimFunc(content.Text, widthx.IsJSSpace); text != "" {
				c.contentContainer.AddChild(tui.NewMarkdownWithOptions(render.SafeText(text), pad, 0, &markdownTheme, nil, &tui.MarkdownOptions{
					Transform: render.CreateMarkdownTransform(render.MessageAssistant, c.isStreaming, c.ctx.MarkdownTransformers(), transformSource(c.ctx)),
				}))
			}
		case ai.ThinkingContent:
			var blocks []string
			for ; i < len(message.Content); i++ {
				thinking, ok := message.Content[i].(ai.ThinkingContent)
				if !ok {
					break
				}
				if text := strings.TrimFunc(thinking.Thinking, widthx.IsJSSpace); text != "" {
					blocks = append(blocks, text)
				}
			}
			i--
			if len(blocks) == 0 {
				continue
			}
			hasVisibleAfter := visibleFrom(i + 1)
			runIndex := thinkingRun
			thinkingRun++
			hidden, overridden := c.thinkingOverrides[runIndex]
			if !overridden {
				hidden = c.hideThinkingBlock
			}
			var thinkingComponent tui.Component
			if hidden {
				thinkingComponent = tui.NewPaddedText(theme.Italic(theme.Fg("thinkingText", c.hiddenThinkingLabel)), pad, 0, nil)
			} else {
				thinkingComponent = tui.NewMarkdownWithOptions(render.SafeText(strings.Join(blocks, "\n\n")), pad, 0, &markdownTheme,
					&tui.DefaultTextStyle{Color: func(text string) string { return theme.Fg("thinkingText", text) }, Italic: true},
					&tui.MarkdownOptions{
						Transform: render.CreateMarkdownTransform(render.MessageAssistantThinking, c.isStreaming, c.ctx.MarkdownTransformers(), transformSource(c.ctx)),
					})
			}
			c.contentContainer.AddChild(tui.NewMouseRegion(thinkingComponent, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
				if event.Type != tui.MouseClick || event.Button != tui.MouseButtonLeft {
					return nil
				}
				c.thinkingOverrides[runIndex] = !hidden
				c.rebuild()
				c.ctx.RequestRender()
				return &tui.TuiMouseEventResult{Handled: true}
			}))
			if hasVisibleAfter {
				c.contentContainer.AddChild(tui.NewSpacer(1))
			}
		}
	}
	// Incomplete/failed: shown after partial content. Tool cards show aborted/failed calls themselves.
	hasToolCalls := false
	for _, block := range message.Content {
		if _, ok := block.(ai.ToolCall); ok {
			hasToolCalls = true
		}
	}
	c.hasToolCalls = hasToolCalls
	notice := func(text string) {
		c.contentContainer.AddChild(tui.NewSpacer(1))
		c.contentContainer.AddChild(tui.NewPaddedText(theme.Fg("error", render.SafeText(text)), pad, 0, nil))
	}
	switch {
	case message.StopReason == ai.StopReasonLength:
		notice("Response was truncated before completion.")
	case hasToolCalls:
	case message.StopReason == ai.StopReasonAborted:
		if message.ErrorMessage != "" && message.ErrorMessage != "Request was aborted" {
			notice(message.ErrorMessage)
		} else {
			notice("Operation aborted")
		}
	case message.StopReason == ai.StopReasonError:
		reason := message.ErrorMessage
		if reason == "" {
			reason = "Unknown error"
		}
		notice("Error: " + reason)
	}
}

// CompactionSummaryComponent is the summary that replaced earlier context: collapsed to one line until expanded (DTUI CompactionComponent).
type CompactionSummaryComponent struct {
	themed
	summary   string
	expanded  bool
	label     string
	collapsed string
}

// CompactionSummaryOptions are the optional texts of a CompactionSummaryComponent: "" selects the default.
type CompactionSummaryOptions struct{ Label, Collapsed string }

// NewCompactionSummaryComponent is the constructor.
func NewCompactionSummaryComponent(summary string, expanded bool, ctx MessageContext, options CompactionSummaryOptions) *CompactionSummaryComponent {
	c := &CompactionSummaryComponent{themed: newThemed(ctx), summary: summary, expanded: expanded, label: options.Label, collapsed: options.Collapsed}
	if c.label == "" {
		c.label = "[compaction]"
	}
	if c.collapsed == "" {
		c.collapsed = "Earlier context summarized"
	}
	c.themed.rebuild = c.rebuild
	c.rebuild()
	return c
}

// SetExpanded expands the summary.
func (c *CompactionSummaryComponent) SetExpanded(expanded bool) {
	if expanded == c.expanded {
		return
	}
	c.expanded = expanded
	c.rebuild()
}

func (c *CompactionSummaryComponent) rebuild() {
	c.Clear()
	theme := c.ctx.Theme().Current()
	box := tui.NewBox()
	box.PaddingX, box.PaddingY = c.ctx.OutputPad(), 1
	box.BgFn = func(text string) string { return theme.Bg("customMessageBg", text) }
	content := tui.NewContainer()
	content.AddChild(tui.NewPaddedText(theme.Fg("customMessageLabel", theme.Bold(c.label)), 0, 0, nil))
	content.AddChild(tui.NewSpacer(1))
	if c.expanded && strings.TrimFunc(c.summary, widthx.IsJSSpace) != "" {
		markdownTheme := c.ctx.Theme().Markdown
		content.AddChild(tui.NewMarkdownWithOptions(render.SafeText(c.summary), 0, 0, &markdownTheme,
			&tui.DefaultTextStyle{Color: func(text string) string { return theme.Fg("customMessageText", text) }}, nil))
	} else {
		content.AddChild(tui.NewPaddedText(theme.Fg("customMessageText", c.collapsed+" (")+theme.Fg("dim", c.ctx.KeyText("app.tools.expand"))+theme.Fg("customMessageText", " to expand)"), 0, 0, nil))
	}
	box.AddChild(tui.NewMouseRegion(content, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseClick || event.Button != tui.MouseButtonLeft {
			return nil
		}
		c.SetExpanded(!c.expanded)
		c.ctx.RequestRender()
		return &tui.TuiMouseEventResult{Handled: true}
	}))
	c.AddChild(box)
}

// NoticeComponent is a one-line muted or error notice in the transcript (DTUI `#addText`, pi's `showError`).
type NoticeComponent struct {
	themed
	text string
	// color is "muted", "error" or "warning".
	color string
}

// NewNoticeComponent is the constructor.
func NewNoticeComponent(text, color string, ctx MessageContext) *NoticeComponent {
	c := &NoticeComponent{themed: newThemed(ctx), text: text, color: color}
	c.themed.rebuild = c.rebuild
	c.rebuild()
	return c
}

// Update shows another text.
func (c *NoticeComponent) Update(text string) {
	if text == c.text {
		return
	}
	c.text = text
	c.rebuild()
}

func (c *NoticeComponent) rebuild() {
	c.Clear()
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewPaddedText(c.ctx.Theme().Current().Fg(c.color, render.SafeText(c.text)), c.ctx.OutputPad(), 0, nil))
}
