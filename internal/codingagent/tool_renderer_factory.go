// Ports packages/coding-agent/src/core/tools/renderers/index.ts.
package codingagent

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// ToolRenderers is the built-in presentation pair, without a tool implementation or parameter schema.
type ToolRenderers struct {
	RenderCall   extension.ToolRenderCallFunc
	RenderResult extension.ToolRenderResultFunc
}

// CreateAllToolRenderers returns the shared built-in render functions, keyed by tool name. Each card supplies its own state and owns its background work.
func CreateAllToolRenderers() map[string]ToolRenderers {
	renderers := make(map[string]ToolRenderers)
	for _, name := range []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"} {
		call, result := builtInToolRenderers(name)
		renderers[name] = ToolRenderers{RenderCall: call, RenderResult: result}
	}
	return renderers
}

// ToolRendererCard owns one built-in card and its asynchronous invalidations. Its Component is mutated and rendered on the presentation owner loop; Dispose runs off-loop, cancels invalidations and joins all ticker/preview work. An already running filesystem preview finishes before Dispose returns.
type ToolRendererCard struct {
	Component   *tui.ToolExecutionComponent
	state       *builtInRenderState
	cancel      context.CancelFunc
	disposeOnce sync.Once
}

// NewToolRendererCard binds shared renderers to an independent card. An empty pair retains the native card's unregistered-tool path. RequestRender must be safe from a background invalidation.
func NewToolRendererCard(ctx context.Context, name, toolCallId, cwd string, args json.RawMessage, renderers ToolRenderers, requestRender func()) *ToolRendererCard {
	lifetime, cancel := context.WithCancel(ctx)
	state := &builtInRenderState{lifetime: lifetime}
	component := tui.NewToolExecutionComponent(name, "")
	component.Cwd = cwd
	card := &ToolRendererCard{Component: component, state: state, cancel: cancel}
	renderState := map[string]any{builtInRenderStateKey: state}
	cardId := "card-" + strconv.FormatUint(toolCardSeq.Add(1), 10)
	invalidate := func() {
		if lifetime.Err() != nil {
			return
		}
		component.Invalidate()
		if requestRender != nil {
			requestRender()
		}
	}
	makeContext := func(input tui.ToolRenderInput, last extension.Component) extension.ToolRenderContext {
		return extension.ToolRenderContext{
			Args: input.Args, ToolCallID: toolCallId, Cwd: cwd, State: renderState, Card: cardId,
			Invalidate: invalidate, LastComponent: last, ExecutionStarted: input.ExecutionStarted,
			ArgsComplete: input.ArgsComplete, IsPartial: input.IsPartial, Expanded: input.Expanded,
			ShowImages: input.ShowImages, IsError: input.IsError,
		}
	}
	definition := &tui.ToolDefinitionRenderers{}
	if renderers.RenderCall != nil {
		var last extension.Component
		definition.Call = func(input tui.ToolRenderInput) (tui.Component, bool) {
			result, ok := runToolRenderer(func() extension.Component {
				return renderers.RenderCall(input.Args, tui.ActiveTheme(), makeContext(input, last))
			})
			last = result
			return result, ok
		}
	}
	if renderers.RenderResult != nil {
		var last extension.Component
		definition.Result = func(input tui.ToolRenderInput) (tui.Component, bool) {
			value := component.ResultValue()
			if value == nil {
				value = agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: component.Output}}, IsError: input.IsError}
			}
			result, ok := runToolRenderer(func() extension.Component {
				return renderers.RenderResult(value, extension.ToolRenderResultOptions{Expanded: input.Expanded, IsPartial: input.IsPartial}, tui.ActiveTheme(), makeContext(input, last))
			})
			last = result
			return result, ok
		}
	}
	if renderers.RenderCall != nil || renderers.RenderResult != nil {
		component.SetDefinition(definition, args)
	} else {
		component.UpdateArgs(name, string(args))
	}
	return card
}

// Dispose cancels the card and joins its existing work. Repeated callers wait for the same disposal. State is fenced before waiting, so rendering cannot start another background task after disposal begins.
func (card *ToolRendererCard) Dispose() {
	card.disposeOnce.Do(func() {
		card.cancel()
		card.state.mu.Lock()
		card.state.closed = true
		if card.state.stopTick != nil {
			close(card.state.stopTick)
			card.state.stopTick = nil
		}
		card.state.mu.Unlock()
		card.state.tasks.Wait()
	})
}
