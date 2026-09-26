package tui

// Ports packages/coding-agent/src/modes/interactive/components/tool-execution.ts
// for a tool with a registered definition: its renderCall and renderResult
// components in the default Box shell or its own framing.

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ToolRenderInput is the card state upstream ToolExecutionComponent passes to
// a registered tool definition's renderers.
type ToolRenderInput struct {
	Args             json.RawMessage
	ExecutionStarted bool
	ArgsComplete     bool
	IsPartial        bool
	Expanded         bool
	ShowImages       bool
	IsError          bool
}

// ToolDefinitionRenderers is a registered tool definition as the card draws
// it. Call and Result run the definition's renderCall and renderResult for
// the card's current state and return the component. They report false when
// the definition has no such renderer or the renderer failed; the card then
// draws upstream's fallback. Self is renderShell "self".
type ToolDefinitionRenderers struct {
	Self   bool
	Call   func(ToolRenderInput) (Component, bool)
	Result func(ToolRenderInput) (Component, bool)
}

// RendererFallback is implemented by a renderer component whose rendering can
// fail after it was returned, because an extension process renders it. The
// card supplies the fallback upstream shows for a renderer that throws.
type RendererFallback interface {
	SetRendererFallback(func(width int) []string)
}

type rendererDirtyReporter interface {
	IsDirty() bool
}

const fallbackResultPreviewLines = 10

// SetDefinition makes the card draw a registered tool definition as upstream
// does, instead of the built-in or generic presentation. args are the call's
// current arguments.
func (c *ToolExecutionComponent) SetDefinition(definition *ToolDefinitionRenderers, args json.RawMessage) {
	c.definition = definition
	if len(args) > 0 && json.Valid(args) {
		c.definitionArgs = append(c.definitionArgs[:0], args...)
	}
	c.Invalidate()
}

// SetDefinitionArgs replaces the arguments the definition's renderers
// receive, as upstream updateArgs does.
func (c *ToolExecutionComponent) SetDefinitionArgs(args json.RawMessage) {
	if len(args) > 0 && json.Valid(args) {
		c.definitionArgs = append(c.definitionArgs[:0], args...)
	}
	c.Invalidate()
}

// HasDefinition reports whether the card draws a registered tool definition.
func (c *ToolExecutionComponent) HasDefinition() bool { return c.definition != nil }

// SetResultValue records the structured tool result, partial while the tool
// runs, that the definition's result renderer receives.
func (c *ToolExecutionComponent) SetResultValue(result any) {
	c.definitionResult = result
	c.Invalidate()
}

// ResultValue returns the structured result recorded by SetResultValue.
func (c *ToolExecutionComponent) ResultValue() any { return c.definitionResult }

// definitionHasResult reports whether upstream's card has a result: a
// streamed partial result or the final one.
func (c *ToolExecutionComponent) definitionHasResult() bool {
	return c.State != ToolStateRunning || c.definitionResult != nil || c.Output != ""
}

func (c *ToolExecutionComponent) definitionInput() ToolRenderInput {
	return ToolRenderInput{
		Args:             c.definitionArgs,
		ExecutionStarted: c.executionStarted,
		ArgsComplete:     c.argsComplete,
		IsPartial:        c.IsPartial,
		Expanded:         !c.Collapsed,
		ShowImages:       c.ShowImages,
		IsError:          c.State == ToolStateError,
	}
}

// updateDefinition is upstream updateDisplay for a card with a definition: it
// runs the renderers for the current state and keeps their components, or
// the fallbacks in place of a missing or failed renderer.
func (c *ToolExecutionComponent) updateDefinition() {
	c.definitionDirty.Store(false)
	input := c.definitionInput()
	c.definitionCall = nil
	if c.definition.Call != nil {
		if component, ok := c.definition.Call(input); ok && component != nil {
			c.definitionCall = component
		}
	}
	if c.definitionCall == nil {
		c.definitionCall = c.callFallback()
	} else if fallback, ok := c.definitionCall.(RendererFallback); ok {
		fallback.SetRendererFallback(c.callFallback().Render)
	}

	c.definitionResultComponent = nil
	if !c.definitionHasResult() {
		return
	}
	if c.definition.Result != nil {
		if component, ok := c.definition.Result(input); ok && component != nil {
			c.definitionResultComponent = component
			if fallback, ok := component.(RendererFallback); ok {
				fallback.SetRendererFallback(func(width int) []string {
					if preview := c.resultFallback(); preview != nil {
						return preview.Render(width)
					}
					return nil
				})
			}
			return
		}
	}
	if preview := c.resultFallback(); preview != nil {
		c.definitionResultComponent = preview
	}
}

// callFallback is upstream createCallFallback: the tool name in toolTitle.
func (c *ToolExecutionComponent) callFallback() Component {
	return NewPaddedText(toolTitleText(c.Name), 0, 0, nil)
}

// resultFallback is upstream createResultFallback: the first ten output
// lines, or all of them when expanded, with a hint for the rest.
func (c *ToolExecutionComponent) resultFallback() Component {
	output := strings.ReplaceAll(widthx.StripAnsi(c.Output), "\r", "")
	if output == "" {
		return nil
	}
	theme := ActiveTheme()
	lines := strings.Split(output, "\n")
	display := lines
	if c.Collapsed && len(lines) > fallbackResultPreviewLines {
		display = lines[:fallbackResultPreviewLines]
	}
	styled := make([]string, len(display))
	for i, line := range display {
		styled[i] = theme.FgText("toolOutput", line)
	}
	text := strings.Join(styled, "\n")
	if remaining := len(lines) - len(display); remaining > 0 {
		text += theme.FgText("muted", "\n... ("+strconv.Itoa(remaining)+" more lines,") + " " +
			theme.FgText("dim", AppKeyText("app.tools.expand", "ctrl+o")) + theme.FgText("muted", " to expand") +
			theme.FgText("muted", ")")
	}
	return NewPaddedText(text, 0, 0, nil)
}

func (c *ToolExecutionComponent) definitionComponents() []Component {
	components := []Component{c.definitionCall}
	if c.definitionResultComponent != nil {
		components = append(components, c.definitionResultComponent)
	}
	return components
}

// definitionComponentsDirty reports a renderer component whose frame changed
// since the card last drew it.
func (c *ToolExecutionComponent) definitionComponentsDirty() bool {
	for _, component := range []Component{c.definitionCall, c.definitionResultComponent} {
		if reporter, ok := component.(rendererDirtyReporter); ok && reporter.IsDirty() {
			return true
		}
	}
	return false
}

// definitionBg is the card's lifecycle background: pending while partial,
// then error or success.
func (c *ToolExecutionComponent) definitionBg() func(string) string {
	theme := ActiveTheme()
	token := "toolSuccessBg"
	switch {
	case c.IsPartial:
		token = "toolPendingBg"
	case c.State == ToolStateError:
		token = "toolErrorBg"
	}
	open := theme.Bg(token)
	return func(text string) string { return open + text + SGRBgReset }
}

// renderDefinition is upstream render for a card with a definition: the
// default shell is a spacer over a padded Box in the lifecycle background;
// renderShell "self" draws the components after one blank line, and nothing
// when they draw nothing. Images follow either shell.
func (c *ToolExecutionComponent) renderDefinition(width int) []string {
	if c.definitionDirty.Load() || c.definitionCall == nil {
		c.updateDefinition()
	}
	images := c.renderImages(width)
	if c.definition.Self {
		var content []string
		for _, component := range c.definitionComponents() {
			content = append(content, component.Render(width)...)
		}
		if len(content) == 0 && len(images) == 0 {
			return []string{}
		}
		out := make([]string, 0, len(content)+len(images)+1)
		if len(content) > 0 {
			out = append(out, "")
			out = append(out, content...)
		}
		return append(out, images...)
	}
	box := NewPaddedBox(1, 1, c.definitionBg())
	for _, component := range c.definitionComponents() {
		box.AddChild(component)
	}
	out := append([]string{""}, box.Render(width)...)
	return append(out, images...)
}
