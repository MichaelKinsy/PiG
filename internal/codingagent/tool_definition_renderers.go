package codingagent

import (
	"encoding/json"
	"strconv"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

var toolCardSeq atomic.Uint64

// applyToolPresentation binds definition renderers and their retained call arguments. Core read/write cards use the same built-in definitions as extension overrides, so presentation never depends on private tool-result metadata.
func (m *InteractiveMode) applyToolPresentation(comp *tui.ToolExecutionComponent, toolCallID, name string, args json.RawMessage) {
	if comp == nil {
		return
	}
	if comp.HasDefinition() {
		comp.SetDefinitionArgs(args)
		return
	}
	var definition extension.ToolDefinition
	var ok bool
	if m.newRunner != nil {
		definition, ok = m.newRunner.GetToolDefinition(name)
	}
	if !ok && (name == "read" || name == "write") {
		definition, ok = extension.ToolDefinition{Name: name}, true
	}
	// A registered definition draws its own card, with fallbacks for the renderers it lacks; only a tool with no
	// definition uses the plain card (upstream ToolExecutionComponent hasRendererDefinition).
	if ok {
		builtIn := name
		if definition.BuiltInRenderers != "" {
			builtIn = definition.BuiltInRenderers
		}
		definition = withBuiltInRenderers(builtIn, definition)
		comp.SetDefinition(m.toolDefinitionRenderers(definition, comp, toolCallID), args)
	}
}

// toolDefinitionRenderers adapts a registered tool definition to a card the
// way upstream ToolExecutionComponent calls it: one renderer state per card
// shared by both renderers, each renderer's last component, and a context
// invalidate that runs both renderers again.
func (m *InteractiveMode) toolDefinitionRenderers(definition extension.ToolDefinition, comp *tui.ToolExecutionComponent, toolCallID string) *tui.ToolDefinitionRenderers {
	state := map[string]any{}
	card := "card-" + strconv.FormatUint(toolCardSeq.Add(1), 10)
	cwd := m.opts.CWD
	invalidate := func() {
		comp.Invalidate()
		if m.tuiInst != nil {
			m.requestRender()
		}
	}
	context := func(input tui.ToolRenderInput, last extension.Component) extension.ToolRenderContext {
		return extension.ToolRenderContext{
			Args:             input.Args,
			ToolCallID:       toolCallID,
			Invalidate:       invalidate,
			LastComponent:    last,
			State:            state,
			Cwd:              cwd,
			ExecutionStarted: input.ExecutionStarted,
			ArgsComplete:     input.ArgsComplete,
			IsPartial:        input.IsPartial,
			Expanded:         input.Expanded,
			ShowImages:       input.ShowImages,
			IsError:          input.IsError,
			Card:             card,
		}
	}
	renderers := &tui.ToolDefinitionRenderers{Self: definition.RenderShell == extension.ToolRenderShellSelf}
	if definition.RenderCall != nil {
		var last extension.Component
		renderers.Call = func(input tui.ToolRenderInput) (tui.Component, bool) {
			component, ok := runToolRenderer(func() extension.Component {
				return definition.RenderCall(input.Args, tui.ActiveTheme(), context(input, last))
			})
			last = component
			return component, ok
		}
	}
	if definition.RenderResult != nil {
		var last extension.Component
		renderers.Result = func(input tui.ToolRenderInput) (tui.Component, bool) {
			result := comp.ResultValue()
			if result == nil {
				result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: comp.Output}}, IsError: input.IsError}
			}
			options := extension.ToolRenderResultOptions{Expanded: input.Expanded, IsPartial: input.IsPartial}
			component, ok := runToolRenderer(func() extension.Component {
				return definition.RenderResult(result, options, tui.ActiveTheme(), context(input, last))
			})
			last = component
			return component, ok
		}
	}
	return renderers
}

// runToolRenderer runs one renderer and reports its component, or false when
// it panicked or returned no component.
func runToolRenderer(render func() extension.Component) (component tui.Component, ok bool) {
	defer func() {
		// upstream: packages/coding-agent/src/modes/interactive/components/tool-execution.ts:updateDisplay
		if recover() != nil {
			component, ok = nil, false
		}
	}()
	rendered, isComponent := render().(tui.Component)
	if !isComponent || rendered == nil {
		return nil, false
	}
	return rendered, true
}
