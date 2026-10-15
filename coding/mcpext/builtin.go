package mcpext

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// piAdapter is the [API] the MCP extension registers on, over the extension API its factory receives. The extension calls the API from
// goroutines it owns (a connection that settles, a server that changes its tool list), after the handler that started them returned;
// a tool it registers then reports a failure to the user through the context of the latest event, since the call has no error to return it to.
type piAdapter struct {
	pi      extension.API
	context atomic.Pointer[extension.Context]
}

func newPiAdapter(pi extension.API) *piAdapter { return &piAdapter{pi: pi} }

// track records the extension context a runner put in ctx.
func (a *piAdapter) track(ctx context.Context) {
	if c := extension.FromContext(ctx); c != nil {
		a.context.Store(c)
	}
}

func (a *piAdapter) OnSessionStart(handler func(ctx context.Context, evt extension.SessionStartEvent) error) {
	a.pi.OnSessionStart(func(ctx context.Context, evt extension.SessionStartEvent) error {
		a.track(ctx)
		return handler(ctx, evt)
	})
}

func (a *piAdapter) OnBeforeAgentStart(handler func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)) {
	a.pi.OnBeforeAgentStart(func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
		a.track(ctx)
		return handler(ctx, evt)
	})
}

func (a *piAdapter) OnToolCall(handler func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error)) {
	a.pi.OnToolCall(func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
		a.track(ctx)
		return handler(ctx, evt)
	})
}

func (a *piAdapter) OnTurnStart(handler func(ctx context.Context, evt extension.TurnStartEvent) error) {
	a.pi.OnTurnStart(func(ctx context.Context, evt extension.TurnStartEvent) error {
		a.track(ctx)
		return handler(ctx, evt)
	})
}

func (a *piAdapter) OnMcpServersChange(handler func(ctx context.Context, evt extension.McpServersChangeEvent) error) {
	a.pi.OnMcpServersChange(func(ctx context.Context, evt extension.McpServersChangeEvent) error {
		a.track(ctx)
		return handler(ctx, evt)
	})
}

func (a *piAdapter) OnSessionShutdown(handler func(ctx context.Context, evt extension.SessionShutdownEvent) error) {
	a.pi.OnSessionShutdown(func(ctx context.Context, evt extension.SessionShutdownEvent) error {
		a.track(ctx)
		return handler(ctx, evt)
	})
}

func (a *piAdapter) RegisterCommand(name string, options extension.CommandOptions) {
	a.pi.RegisterCommand(name, options)
}

func (a *piAdapter) RegisterToolRenderer(resolver extension.ToolRendererResolver) {
	a.pi.RegisterToolRenderer(resolver)
}

// RegisterTool registers the tool, which refreshes the Session's tool registry once a Session runs. A failure to admit the tool is shown
// to the user, since the caller has no error to return it to.
//
// upstream: core/extensions/loader.ts:273-284 (registerTool)
func (a *piAdapter) RegisterTool(definition extension.ToolDefinition) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// upstream: registerTool throws; the extension has no caller to throw to, so the failure reaches the user.
			if c := a.context.Load(); c != nil {
				if ui, err := c.UI(); err == nil {
					ui.Notify(fmt.Sprintf("MCP tool %s could not be registered: %v", definition.Name, recovered), "error")
				}
			}
		}
	}()
	a.pi.RegisterTool(definition)
}

func (a *piAdapter) GetAllTools() []extension.ToolInfo { return a.pi.GetAllTools() }

func (a *piAdapter) GetActiveTools() []string { return a.pi.GetActiveTools() }

func (a *piAdapter) SetActiveTools(names []string) { a.pi.SetActiveTools(names) }

func (a *piAdapter) GetMcpServers() []extension.RegisteredMcpServer { return a.pi.GetMcpServers() }
