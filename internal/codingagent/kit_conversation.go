package codingagent

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// An extension's view of Pi's conversation components (D107) draws its tool cards as the main transcript draws a
// tool without renderers of its own, and reports their diffs as the transcript does.
func init() {
	subprocess.SetConversationRenderers(subprocess.ConversationRenderers{
		NewToolCard: newKitToolCard,
		ToolDiff:    toolResultDiff,
	})
}

// newKitToolCard is the card of a kit tool-execution node: the built-in tool definition's renderers for
// ViewToolDefinitionBuiltin and a built-in name, otherwise a definition without renderers, whose call and result
// fallbacks upstream ToolExecutionComponent draws.
func newKitToolCard(ctx context.Context, toolName, toolCallID, cwd string, args json.RawMessage, toolDefinition string, requestRender func()) (*tui.ToolExecutionComponent, func()) {
	var renderers ToolRenderers
	if toolDefinition == subprocess.ViewToolDefinitionBuiltin {
		renderers = CreateAllToolRenderers()[toolName]
	}
	card := NewToolRendererCard(ctx, toolName, toolCallID, cwd, args, renderers, requestRender)
	if !card.Component.HasDefinition() {
		card.Component.SetDefinition(&tui.ToolDefinitionRenderers{}, args)
	}
	return card.Component, card.Dispose
}
