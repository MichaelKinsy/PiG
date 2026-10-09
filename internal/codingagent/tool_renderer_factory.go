// Ports packages/coding-agent/src/core/tools/renderers/index.ts.
package codingagent

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// ToolRenderers is types.ts ToolRenderers (renderShell, renderCall and renderResult of a tool definition): the built-in presentation, without a tool implementation or parameter schema.
type ToolRenderers = extension.ToolRenderers

// CreateAllToolRenderers returns the shared built-in render functions, keyed by tool name. Each card supplies its own state and owns its background work.
func CreateAllToolRenderers() map[string]ToolRenderers {
	renderers := make(map[string]ToolRenderers)
	for _, name := range []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"} {
		call, result := builtInToolRenderers(name)
		renderers[name] = ToolRenderers{RenderShell: builtInRenderShell(name), RenderCall: call, RenderResult: result}
	}
	return renderers
}

// builtInRenderShell is the built-in tool's renderShell: only edit draws its own framing (core/tools/edit.ts:157).
func builtInRenderShell(name string) extension.ToolRenderShell {
	if name == "edit" {
		return extension.ToolRenderShellSelf
	}
	return extension.ToolRenderShellDefault
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
	component := tui.NewToolExecutionComponent(name, toolCallId, nil, tui.ToolExecutionOptions{}, nil, nil, cwd)
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
			Args: input.Args, ToolCallID: input.ToolCallID, Cwd: cwd, State: renderState, Card: cardId,
			Invalidate: invalidate, LastComponent: last, ExecutionStarted: input.ExecutionStarted,
			ArgsComplete: input.ArgsComplete, IsPartial: input.IsPartial, Expanded: input.Expanded,
			ShowImages: input.ShowImages, IsError: input.IsError, OutputPad: input.OutputPad, DurationMs: input.DurationMs,
		}
	}
	definition := &tui.ToolDefinitionRenderers{Self: renderers.RenderShell == extension.ToolRenderShellSelf}
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
			value, ok := toolResultOf(component.ResultValue())
			if !ok {
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
		component.UpdateArgs(args)
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

// ToolExecutionResultOf converts an agent tool result to the argument of tui.ToolExecutionComponent.UpdateResult. The agent
// result itself travels as Value so a definition's result renderer receives the tool's own object.
func ToolExecutionResultOf(result agent.AgentToolResult, elapsed time.Duration) tui.ToolResultUpdate {
	out := tui.ToolResultUpdate{Details: result.Details, IsError: result.IsError, Elapsed: elapsed, Result: result}
	for _, block := range result.Content {
		switch value := block.(type) {
		case ai.TextContent:
			out.Content = append(out.Content, tui.ToolResultContent{Type: "text", Text: value.Text})
		case ai.ImageContent:
			out.Content = append(out.Content, tui.ToolResultContent{Type: "image", Data: value.Data, MimeType: value.MimeType})
		}
	}
	return out
}
