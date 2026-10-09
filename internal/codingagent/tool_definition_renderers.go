package codingagent

import (
	"encoding/json"
	"strconv"
	"sync/atomic"
	"weak"

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
	m.presentToolCard(comp, toolCallID, name, args)
}

// toolCardRecord is a tool card drawn through resolved renderers, kept weakly so a resolution an extension process
// answers later can draw the card again.
type toolCardRecord struct {
	card weak.Pointer[tui.ToolExecutionComponent]
}

// reapplyToolPresentation draws the cards of toolName again after an extension process answered a tool renderer
// resolution with other renderers than next()'s (D89). It runs on the owner loop.
func (m *InteractiveMode) reapplyToolPresentation(toolName string) {
	for toolCallID, record := range m.toolCards[toolName] {
		comp := record.card.Value()
		if comp == nil {
			delete(m.toolCards[toolName], toolCallID)
			continue
		}
		m.bindToolCard(comp, toolCallID, toolName, nil)
	}
	if len(m.toolCards[toolName]) == 0 {
		delete(m.toolCards, toolName)
	}
	if m.tuiInst != nil {
		m.requestRender()
	}
}

// presentToolCard binds the card to the renderers resolved for name and records it for a later resolution.
func (m *InteractiveMode) presentToolCard(comp *tui.ToolExecutionComponent, toolCallID, name string, args json.RawMessage) {
	m.bindToolCard(comp, toolCallID, name, args)
	m.recordToolCard(comp, toolCallID, name)
}

// resolveToolRenderers returns the renderers that draw calls of the named tool, or nil when the tool has none.
func (m *InteractiveMode) resolveToolRenderers(name string) *extension.ToolRenderers {
	// upstream: interactive-mode.ts getRegisteredToolDefinition: extension resolvers in load order, then the registered tool.
	base := func() *extension.ToolRenderers {
		var definition extension.ToolDefinition
		var ok bool
		if m.newRunner != nil {
			definition, ok = m.newRunner.GetToolDefinition(name)
		}
		if !ok && (name == "read" || name == "write" || name == "edit") {
			definition, ok = extension.ToolDefinition{Name: name}, true
			if name == "edit" {
				// upstream: core/tools/edit.ts renderShell: "self"
				definition.RenderShell = extension.ToolRenderShellSelf
			}
		}
		if !ok {
			return nil
		}
		builtIn := name
		if definition.BuiltInRenderers != "" {
			builtIn = definition.BuiltInRenderers
		}
		definition = withBuiltInRenderers(builtIn, definition)
		return &extension.ToolRenderers{RenderShell: definition.RenderShell, RenderCall: definition.RenderCall, RenderResult: definition.RenderResult}
	}
	if m.newRunner != nil {
		return m.newRunner.ResolveToolRenderers(name, base)
	}
	return base()
}

// toolCardDefinition is the registered tool's renderers bound to the card they draw: the ToolDefinition arm of upstream's
// `ToolRenderers | ToolDefinition` that a card receives at construction (tool-execution.ts:65-73).
type toolCardDefinition struct {
	m          *InteractiveMode
	renderers  extension.ToolRenderers
	toolCallID string
	card       *tui.ToolExecutionComponent
}

// ToolRenderers adapts the registered renderers to the card's render inputs.
func (d *toolCardDefinition) ToolRenderers() *tui.ToolDefinitionRenderers {
	return d.m.toolDefinitionRenderers(d.renderers, func() *tui.ToolExecutionComponent { return d.card }, d.toolCallID)
}

// newToolCard returns the card of one tool call, built with the registered definition that draws it (nil when the tool
// has none), and records it for a later renderer resolution.
func (m *InteractiveMode) newToolCard(name, toolCallID string, args json.RawMessage) *tui.ToolExecutionComponent {
	var source tui.ToolDefinitionSource
	var definition *toolCardDefinition
	if renderers := m.resolveToolRenderers(name); renderers != nil {
		definition = &toolCardDefinition{m: m, renderers: *renderers, toolCallID: toolCallID}
		source = definition
	}
	comp := tui.NewToolExecutionComponent(name, toolCallID, args, m.toolExecutionOptions(), source, m.tuiInst, m.opts.CWD)
	if definition != nil {
		definition.card = comp
	}
	m.recordToolCard(comp, toolCallID, name)
	return comp
}

// bindToolCard binds the card to the renderers resolved for name.
func (m *InteractiveMode) bindToolCard(comp *tui.ToolExecutionComponent, toolCallID, name string, args json.RawMessage) {
	renderers := m.resolveToolRenderers(name)
	// Resolved renderers draw their own card, with fallbacks for the renderers they lack; only a tool with none uses
	// the plain card (upstream ToolExecutionComponent hasRendererDefinition). A later resolution without renderers
	// returns a card to the plain card.
	switch {
	case renderers != nil:
		comp.SetDefinition(m.toolDefinitionRenderers(*renderers, func() *tui.ToolExecutionComponent { return comp }, toolCallID), args)
	case comp.HasDefinition():
		comp.SetDefinition(nil, args)
	}
}

// recordToolCard keeps a weak reference to the card for reapplyToolPresentation. Records of collected cards are swept
// once the records double, so they stay proportional to the live cards.
func (m *InteractiveMode) recordToolCard(comp *tui.ToolExecutionComponent, toolCallID, name string) {
	if m.toolCards == nil {
		m.toolCards = map[string]map[string]toolCardRecord{}
	}
	if m.toolCards[name] == nil {
		m.toolCards[name] = map[string]toolCardRecord{}
	}
	if _, ok := m.toolCards[name][toolCallID]; !ok {
		m.toolCardRecords++
	}
	m.toolCards[name][toolCallID] = toolCardRecord{card: weak.Make(comp)}
	if m.toolCardRecords <= m.toolCardSweepAt {
		return
	}
	m.toolCardRecords = 0
	for tool, cards := range m.toolCards {
		for id, record := range cards {
			if record.card.Value() == nil {
				delete(cards, id)
			}
		}
		if len(cards) == 0 {
			delete(m.toolCards, tool)
		}
		m.toolCardRecords += len(cards)
	}
	m.toolCardSweepAt = 2*m.toolCardRecords + toolCardSweepMin
}

// toolCardSweepMin is the record count below which collected tool card records are not swept.
const toolCardSweepMin = 256

// toolDefinitionRenderers adapts a registered tool definition to a card the
// way upstream ToolExecutionComponent calls it: one renderer state per card
// shared by both renderers, each renderer's last component, and a context
// invalidate that runs both renderers again.
func (m *InteractiveMode) toolDefinitionRenderers(definition extension.ToolRenderers, card func() *tui.ToolExecutionComponent, toolCallID string) *tui.ToolDefinitionRenderers {
	state := map[string]any{}
	cardID := "card-" + strconv.FormatUint(toolCardSeq.Add(1), 10)
	cwd := m.opts.CWD
	invalidate := func() {
		card().Invalidate()
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
			OutputPad:        input.OutputPad,
			DurationMs:       input.DurationMs,
			Card:             cardID,
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
			result, ok := toolResultOf(card().ResultValue())
			if !ok {
				result = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: card().Output}}, IsError: input.IsError}
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
	rendered := render()
	if rendered == nil {
		return nil, false
	}
	return rendered, true
}
