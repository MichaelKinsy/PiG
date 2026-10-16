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
			a.notifyError(fmt.Sprintf("MCP tool %s could not be registered: %v", definition.Name, recovered))
		}
	}()
	a.pi.RegisterTool(definition)
}

// toolBatchRegistrar is a runner that registers several tools as one change (factoryload's RegisterTools).
type toolBatchRegistrar interface {
	RegisterTools(tools []extension.ToolDefinition) []error
}

// RegisterTools registers the tools as one change when the runner can, so no prompt sees part of a server's tools, as none can between
// the synchronous registerTool calls of Pi's registerTools. A runner without the batch gets one RegisterTool per tool. A tool the runner
// rejects is shown to the user, as RegisterTool shows it.
func (a *piAdapter) RegisterTools(definitions []extension.ToolDefinition) {
	batch, ok := a.pi.(toolBatchRegistrar)
	if !ok {
		for _, definition := range definitions {
			a.RegisterTool(definition)
		}
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			a.notifyError(fmt.Sprintf("MCP tools could not be registered: %v", recovered))
		}
	}()
	for i, err := range batch.RegisterTools(definitions) {
		if err != nil {
			a.notifyError(fmt.Sprintf("MCP tool %s could not be registered: %v", definitions[i].Name, err)) //perf:ok one notice per rejected tool, which only an invalid server schema produces
		}
	}
}

// notifyError shows message to the user of the context the runner last put in an event.
func (a *piAdapter) notifyError(message string) {
	if c := a.context.Load(); c != nil {
		if ui, err := c.UI(); err == nil {
			ui.Notify(message, "error")
		}
	}
}

func (a *piAdapter) GetAllTools() []extension.ToolInfo { return a.pi.GetAllTools() }

func (a *piAdapter) GetActiveTools() []string { return a.pi.GetActiveTools() }

func (a *piAdapter) SetActiveTools(names []string) { a.pi.SetActiveTools(names) }

func (a *piAdapter) GetMcpServers() []extension.RegisteredMcpServer { return a.pi.GetMcpServers() }
