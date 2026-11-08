package components

// Copied from coding-agent interactive/components/tool-execution.ts. p2 drives a card from the transcript's ToolView (call, live slot and result
// joined, src/web/transcript.ts) instead of agent events; images stay text placeholders (no PNG transcoder, section 11); a tool without renderers
// uses pi's generic call/result fallbacks.
//
// pi: apps/p2/src/tui/components/tool-execution.ts

import (
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/tui"
)

const fallbackPreviewLines = 10

// ToolCardContext is what a tool card needs besides the render context.
type ToolCardContext interface {
	render.RenderContext
	// Cwd is the session cwd, for relative path display.
	Cwd() string
	// OutputPad is the horizontal padding of output; implementations return 1 when the visitor set none.
	OutputPad() int
}

// ToolExecutionComponent is one tool call's card.
type ToolExecutionComponent struct {
	*tui.Container
	contentBox              *tui.Box
	selfRenderContainer     *tui.Container
	callRendererComponent   tui.Component
	resultRendererComponent tui.Component
	rendererState           map[string]any
	tool                    transcript.ToolView
	argsComplete            bool
	expanded                bool
	renderers               tools.ToolRenderers
	ctx                     ToolCardContext
	hideComponent           bool
	themeVersion            int
	// invalidated is a renderer's invalidation (its timer's goroutine) that the next render rebuilds the display for.
	invalidated atomic.Bool
	// nested are the calls `calls` read last, for the view they were read from.
	nested     []transcript.NestedCall
	nestedRead bool
}

// NewToolExecutionComponent is the constructor; renderers may be nil. argsComplete is false while the call streams.
func NewToolExecutionComponent(tool transcript.ToolView, renderers *tools.ToolRenderers, ctx ToolCardContext, argsComplete bool) *ToolExecutionComponent {
	c := &ToolExecutionComponent{Container: tui.NewContainer(), rendererState: map[string]any{}, tool: tool, argsComplete: argsComplete, ctx: ctx, themeVersion: ctx.Theme().Version}
	if renderers != nil {
		c.renderers = *renderers
	}
	c.AddChild(tui.NewSpacer(1))
	c.contentBox = tui.NewBox()
	c.contentBox.PaddingX, c.contentBox.PaddingY = 1, 1
	c.contentBox.BgFn = func(text string) string { return ctx.Theme().Current().Bg("toolPendingBg", text) }
	c.selfRenderContainer = tui.NewContainer()
	if c.renderers.RenderShell == "self" {
		c.AddChild(c.selfRenderContainer)
	} else {
		c.AddChild(c.contentBox)
	}
	c.updateDisplay()
	return c
}

// ToolID is the call's ID.
func (c *ToolExecutionComponent) ToolID() string { return c.tool.ID }

// Update shows a new view of the same call (arguments, live slot, result).
func (c *ToolExecutionComponent) Update(tool transcript.ToolView, argsComplete bool) {
	c.tool = tool
	c.argsComplete = argsComplete
	c.nestedRead = false
	c.updateDisplay()
}

// SetExpanded expands the card.
func (c *ToolExecutionComponent) SetExpanded(expanded bool) {
	if expanded == c.expanded {
		return
	}
	c.expanded = expanded
	c.updateDisplay()
}

// Dispose stops renderer timers (a running shell card's elapsed clock).
func (c *ToolExecutionComponent) Dispose() {
	if stop, ok := c.rendererState["interval"].(func()); ok {
		stop()
	}
	c.rendererState["interval"] = nil
}

// calls are the calls this call made: as its tool reads them (`ToolRenderers.nestedCalls`), else as Durable shows them.
func (c *ToolExecutionComponent) calls() []transcript.NestedCall {
	own := c.renderers.NestedCalls
	if own == nil {
		return c.tool.Calls
	}
	if !c.nestedRead {
		c.nested, c.nestedRead = own(c.tool), true
	}
	return c.nested
}

func (c *ToolExecutionComponent) isPartial() bool {
	return c.tool.Status == transcript.ToolPending || c.tool.Status == transcript.ToolRunning
}

func (c *ToolExecutionComponent) isError() bool {
	return c.tool.Status == transcript.ToolError || c.tool.Status == transcript.ToolInterrupted
}

// result is the result as pi's card holds it: absent (nil) until the call runs; partial while running.
func (c *ToolExecutionComponent) result() *tools.ToolResultView {
	tool := c.tool
	if tool.Status == transcript.ToolPending && len(c.calls()) == 0 {
		return nil
	}
	text := tool.Output
	if text == "" && tool.Status == transcript.ToolInterrupted {
		text = "Not run: the answer was interrupted."
	}
	var content []ai.ToolResultMessageContent
	if text != "" {
		content = append(content, ai.TextContent{Text: text})
	}
	for _, image := range tool.Images {
		content = append(content, image)
	}
	return &tools.ToolResultView{Content: content, Details: tool.Details}
}

func (c *ToolExecutionComponent) renderContext(last tui.Component) *tools.ToolRenderContext {
	context := &tools.ToolRenderContext{
		Args:       c.tool.Args,
		ToolCallID: c.tool.ID,
		// A renderer's timer (a running command's elapsed time) invalidates from its own goroutine: the caches drop at once (the container's lock), the card
		// rebuilds its display at its next render, on the owner loop, which also reads it.
		Invalidate: func() {
			c.Container.Invalidate()
			c.invalidated.Store(true)
			c.ctx.RequestRender()
		},
		LastComponent:    last,
		State:            c.rendererState,
		Cwd:              c.ctx.Cwd(),
		ExecutionStarted: c.tool.Status != transcript.ToolPending,
		ArgsComplete:     c.argsComplete,
		IsPartial:        c.isPartial(),
		Expanded:         c.expanded,
		ShowImages:       false,
		IsError:          c.isError(),
		OutputPad:        c.ctx.OutputPad(),
		Calls:            c.calls(),
		UI:               c.ctx,
	}
	if !c.isPartial() {
		context.DurationMs = c.tool.DurationMs
	}
	return context
}

func (c *ToolExecutionComponent) createCallFallback() tui.Component {
	return tui.NewPaddedText(tools.FormatToolCallWithArgs(c.tool.Name, c.tool.Args, c.ctx.Theme().Current(), c.expanded), 0, 0, nil)
}

func (c *ToolExecutionComponent) createResultFallback(result *tools.ToolResultView) tui.Component {
	theme := c.ctx.Theme().Current()
	output := tools.GetTextOutput(result)
	if output == "" {
		return nil
	}
	lines := strings.Split(output, "\n")
	display := lines
	if !c.expanded && len(lines) > fallbackPreviewLines {
		display = lines[:fallbackPreviewLines]
	}
	remaining := len(lines) - len(display)
	styled := make([]string, len(display))
	for i, line := range display {
		styled[i] = theme.Fg("toolOutput", line)
	}
	text := strings.Join(styled, "\n")
	if remaining > 0 {
		text += "\n" + tools.MoreLinesHint(c.ctx, theme, remaining, "")
	}
	return tui.NewPaddedText(text, 0, 0, nil)
}

func (c *ToolExecutionComponent) createResultRegion(component tui.Component) *tui.MouseRegion {
	return tui.NewMouseRegion(component, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if c.result() == nil || event.Type != tui.MouseClick || event.Button != tui.MouseButtonLeft {
			return nil
		}
		c.SetExpanded(!c.expanded)
		c.ctx.RequestRender()
		return &tui.TuiMouseEventResult{Handled: true}
	})
}

// Invalidate drops the caches and rebuilds the display.
func (c *ToolExecutionComponent) Invalidate() {
	c.Container.Invalidate()
	c.updateDisplay()
}

// Render renders the card.
func (c *ToolExecutionComponent) Render(width int) []string {
	if c.invalidated.Swap(false) {
		c.Invalidate()
	}
	// Colors are baked into the content: rebuild after a theme swap (section 5.4).
	if c.themeVersion != c.ctx.Theme().Version {
		c.themeVersion = c.ctx.Theme().Version
		c.updateDisplay()
	}
	if c.hideComponent {
		return nil
	}
	if c.renderers.RenderShell == "self" {
		content := c.selfRenderContainer.Render(width)
		if len(content) > 0 {
			return append([]string{""}, content...)
		}
		return nil
	}
	return c.Container.Render(width)
}

// callOf runs renderCall; a panic is the TypeScript's caught throw (nil).
func (c *ToolExecutionComponent) callOf() (call tui.Component) {
	defer func() {
		// upstream: apps/p2/src/tui/components/tool-execution.ts:renderCall
		if recover() != nil {
			c.callRendererComponent, call = nil, nil
		}
	}()
	call = c.renderers.RenderCall(c.tool.Args, c.ctx.Theme().Current(), c.renderContext(c.callRendererComponent))
	c.callRendererComponent = call
	return call
}

// resultOf runs renderResult; a panic falls back to the generic result.
func (c *ToolExecutionComponent) resultOf(result *tools.ToolResultView) (component tui.Component) {
	defer func() {
		// upstream: apps/p2/src/tui/components/tool-execution.ts:renderResult
		if recover() != nil {
			c.resultRendererComponent = nil
			component = c.createResultFallback(result)
		}
	}()
	component = c.renderers.RenderResult(*result, tools.ToolRenderResultOptions{Expanded: c.expanded, IsPartial: c.isPartial()}, c.ctx.Theme().Current(), c.renderContext(c.resultRendererComponent))
	c.resultRendererComponent = component
	return component
}

func (c *ToolExecutionComponent) updateDisplay() {
	// A parent container reuses a clean child's lines: the card's own rebuild (a new tool state, expanding) is a change it must show.
	defer c.Container.Invalidate()
	theme := c.ctx.Theme().Current()
	result := c.result()
	bg := "toolSuccessBg"
	switch {
	case c.isPartial():
		bg = "toolPendingBg"
	case c.isError():
		bg = "toolErrorBg"
	}
	bgFn := func(text string) string { return theme.Bg(bg, text) }
	var container interface {
		Clear()
		AddChild(tui.Component)
	} = c.selfRenderContainer
	if c.renderers.RenderShell != "self" {
		c.contentBox.SetBgFn(bgFn)
		c.contentBox.SetPaddingX(c.ctx.OutputPad())
		container = c.contentBox
	}
	container.Clear()
	var call tui.Component
	if c.renderers.RenderCall != nil {
		call = c.callOf()
	}
	if call == nil {
		call = c.createCallFallback()
	}
	container.AddChild(c.createResultRegion(call))
	c.hideComponent = false
	if result != nil {
		var component tui.Component
		if c.renderers.RenderResult != nil {
			component = c.resultOf(result)
		} else {
			component = c.createResultFallback(result)
		}
		if component != nil {
			container.AddChild(c.createResultRegion(component))
		}
	}
}
