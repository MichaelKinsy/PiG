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
	// ToolCallID is the card's tool call id (tool-execution.ts toolCallId).
	ToolCallID       string
	Args             json.RawMessage
	ExecutionStarted bool
	ArgsComplete     bool
	IsPartial        bool
	Expanded         bool
	ShowImages       bool
	IsError          bool
	OutputPad        int
	// DurationMs is the recorded execution time of a final result in milliseconds; nil while the result is partial.
	// upstream: tool-execution.ts:137 durationMs: this.isPartial ? undefined : this.result?.durationMs
	DurationMs *int64
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
// current arguments. A nil definition returns the card to that presentation.
func (c *ToolExecutionComponent) SetDefinition(definition *ToolDefinitionRenderers, args json.RawMessage) {
	c.definition = definition
	if len(args) > 0 && json.Valid(args) {
		c.definitionArgs = append(c.definitionArgs[:0], args...)
	}
	c.Invalidate()
}

// SetStructuredArgs draws the card as a registered tool definition without renderers, whose fallback call header shows
// args.
//
// Deprecated: use SetDefinition(&ToolDefinitionRenderers{}, args).
func (c *ToolExecutionComponent) SetStructuredArgs(args json.RawMessage) {
	c.SetDefinition(&ToolDefinitionRenderers{}, args)
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
	var durationMs *int64
	if !c.IsPartial {
		durationMs = c.DurationMs
	}
	return ToolRenderInput{
		ToolCallID:       c.ToolCallID,
		DurationMs:       durationMs,
		Args:             c.definitionArgs,
		ExecutionStarted: c.executionStarted,
		ArgsComplete:     c.argsComplete,
		IsPartial:        c.IsPartial,
		Expanded:         !c.Collapsed,
		ShowImages:       c.ShowImages,
		IsError:          c.State == ToolStateError,
		OutputPad:        c.outputPad,
	}
}

// updateDefinition is upstream updateDisplay for a card with a definition: it
// runs the renderers for the current state and keeps their components, or
// the fallbacks in place of a missing or failed renderer.
func (c *ToolExecutionComponent) updateDefinition() {
	c.definitionDirty.Store(false)
	defer c.fillDefinitionShell()
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

// fillDefinitionShell replaces the shell container's children with the current call and result regions (tool-execution.ts updateDisplay: clear, then addChild).
func (c *ToolExecutionComponent) fillDefinitionShell() {
	components := c.definitionComponents()
	if c.definition.Self {
		c.selfRenderContainer.SetChildren(components...)
		return
	}
	c.contentBox.Clear()
	for _, component := range components {
		c.contentBox.AddChild(component)
	}
}

// callFallback is upstream createCallFallback: the tool name in toolTitle followed by its arguments, on the title line
// while collapsed and one per line when expanded (tool-execution.ts:155-157).
func (c *ToolExecutionComponent) callFallback() Component {
	return NewPaddedText(FormatToolCallWithArgs(c.Name, c.definitionArgs, ActiveTheme(), !c.Collapsed), 0, 0, nil)
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
		styled[i] = theme.Fg("toolOutput", line)
	}
	text := strings.Join(styled, "\n")
	if remaining := len(lines) - len(display); remaining > 0 {
		text += theme.Fg("muted", "\n... ("+strconv.Itoa(remaining)+" more lines,") + " " +
			theme.Fg("dim", AppKeyText("app.tools.expand", "ctrl+o")) + theme.Fg("muted", " to expand") +
			theme.Fg("muted", ")")
	}
	return NewPaddedText(text, 0, 0, nil)
}

// definitionComponents wraps the call and result components in the regions that toggle the card on click (createResultRegion).
func (c *ToolExecutionComponent) definitionComponents() []Component {
	components := []Component{NewMouseRegion(c.definitionCall, c.handleResultMouse)}
	if c.definitionResultComponent != nil {
		components = append(components, NewMouseRegion(c.definitionResultComponent, c.handleResultMouse))
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
	open := theme.GetBgAnsi(token)
	return func(text string) string { return open + text + SGRBgReset }
}

// renderSelfShell is upstream render for renderShell "self": the components after one blank row, or nothing when they draw nothing. Images follow.
func (c *ToolExecutionComponent) renderSelfShell(width int) []string {
	images := c.renderImages(width)
	content := c.selfRenderContainer.Render(width)
	c.mouseChild, c.mouseWidth, c.mouseHeight = c.selfRenderContainer, width, len(content)
	if len(content) == 0 && len(images) == 0 {
		return []string{}
	}
	var out []string
	if len(content) > 0 {
		out = append([]string{""}, content...)
	}
	return append(out, images...)
}
