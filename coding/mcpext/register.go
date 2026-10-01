package mcpext

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// API is the part of the extension API the built-in extension registers on: the events upstream's index.ts subscribes
// to, the `/mcp` command, the tool registry and the MCP server registry (`registerMcpServer` and its `mcp_servers_change`
// event).
type API interface {
	OnSessionStart(handler func(ctx context.Context, evt extension.SessionStartEvent) error)
	OnBeforeAgentStart(handler func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error))
	OnToolCall(handler func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error))
	OnTurnStart(handler func(ctx context.Context, evt extension.TurnStartEvent) error)
	OnMcpServersChange(handler func(ctx context.Context, evt extension.McpServersChangeEvent) error)
	OnSessionShutdown(handler func(ctx context.Context, evt extension.SessionShutdownEvent) error)
	RegisterCommand(name string, options extension.CommandOptions)
	RegisterTool(tool extension.ToolDefinition)
	GetAllTools() []extension.ToolInfo
	GetActiveTools() []string
	SetActiveTools(names []string)
	GetMcpServers() []extension.RegisteredMcpServer
}

// apiHost adapts an API to the [Host] the extension uses.
type apiHost struct{ api API }

func (h apiHost) RegisterTool(definition extension.ToolDefinition) { h.api.RegisterTool(definition) }
func (h apiHost) GetAllTools() []extension.ToolInfo                { return h.api.GetAllTools() }
func (h apiHost) GetActiveTools() []string                         { return h.api.GetActiveTools() }
func (h apiHost) SetActiveTools(names []string)                    { h.api.SetActiveTools(names) }
func (h apiHost) GetMcpServers() []extension.RegisteredMcpServer   { return h.api.GetMcpServers() }

// providerKeys is the part of the session's model registry that resolves the token of a provider (`/login <provider>`).
type providerKeys interface {
	GetAPIKeyForProvider(ctx context.Context, provider string) *string
}

// eventContext builds the handler context from the extension context a runner
// puts in ctx.
func eventContext(ctx context.Context) EventContext {
	c := extension.FromContext(ctx)
	if c == nil {
		return EventContext{}
	}
	cwd, _ := c.CWD()
	return EventContext{
		Cwd: cwd,
		IsProjectTrusted: func() bool {
			trusted, err := c.IsProjectTrusted()
			return err == nil && trusted
		},
		ProviderToken: func(ctx context.Context, provider string) string {
			registry, err := c.ModelRegistry()
			if err != nil {
				return ""
			}
			keys, ok := registry.(providerKeys)
			if !ok {
				return ""
			}
			if key := keys.GetAPIKeyForProvider(ctx, provider); key != nil {
				return *key
			}
			return ""
		},
		Notify: func(message, level string) {
			if ui, err := c.UI(); err == nil {
				ui.Notify(message, level)
			}
		},
	}
}

// Factory returns the extension factory for `builtin:mcp`: it subscribes the
// extension's handlers, exactly the events upstream's index.ts subscribes to.
// It returns the extension so a host can reach its actions.
func Factory(options Options) func(api API) *Extension {
	return func(api API) *Extension {
		e := New(apiHost{api}, options)
		api.OnSessionStart(func(ctx context.Context, _ extension.SessionStartEvent) error {
			e.SessionStart(eventContext(ctx))
			return nil
		})
		api.OnBeforeAgentStart(func(ctx context.Context, _ extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
			e.BeforeAgentStart(eventContext(ctx), extension.BeforeAgentStartOptions(ctx))
			return extension.BeforeAgentStartEventResult{}, nil
		})
		api.OnToolCall(func(ctx context.Context, event extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
			if custom, ok := event.(extension.CustomToolCallEvent); ok {
				e.ToolCall(ctx, custom.ToolName, custom.Input)
			}
			return extension.ToolCallEventResult{}, nil
		})
		api.OnTurnStart(func(ctx context.Context, _ extension.TurnStartEvent) error {
			e.TurnStart(eventContext(ctx))
			return nil
		})
		api.OnMcpServersChange(func(ctx context.Context, _ extension.McpServersChangeEvent) error {
			e.McpServersChange(eventContext(ctx))
			return nil
		})
		api.RegisterCommand("mcp", extension.CommandOptions{
			Description: "Manage MCP servers: sign in, reconnect, enable or disable, and change exposure",
			GetArgumentCompletions: func(prefix string) ([]extension.AutocompleteItem, error) {
				return e.CompleteCommand(prefix), nil
			},
			Handler: func(ctx context.Context, args string) error {
				return e.RunCommand(ctx, args, commandContext(ctx))
			},
		})
		api.OnSessionShutdown(func(context.Context, extension.SessionShutdownEvent) error {
			e.SessionShutdown()
			return nil
		})
		return e
	}
}

// commandContext builds the command context from the extension context a runner puts in ctx.
func commandContext(ctx context.Context) CommandContext {
	c := extension.FromContext(ctx)
	out := CommandContext{EventContext: eventContext(ctx)}
	if c == nil {
		return out
	}
	if mode, err := c.Mode(); err == nil {
		out.Mode = mode
	}
	if hasUI, err := c.HasUI(); err == nil {
		out.HasUI = hasUI
	}
	ui, err := c.UI()
	if err != nil {
		return out
	}
	out.Select = func(ctx context.Context, title string, options []string) (string, bool) {
		choice, err := ui.Select(ctx, title, options, nil)
		return choice, err == nil && choice != ""
	}
	out.Input = func(ctx context.Context, title, placeholder string) (string, bool) {
		value, err := ui.Input(ctx, title, placeholder, nil)
		return value, err == nil && value != ""
	}
	out.ShowManager = func(ctx context.Context, manage func(McpUi) error) error {
		return showManager(ctx, ui, out.EventContext, manage)
	}
	return out
}

// showManager runs manage in the manager view until it returns (showMcpManager).
// upstream: packages/coding-agent/src/extensions/mcp/ui.ts:showMcpManager
func showManager(ctx context.Context, ui extension.UIContext, events EventContext, manage func(McpUi) error) error {
	var running sync.WaitGroup
	defer running.Wait()
	_, err := ui.Custom(ctx, extension.CustomFactory(func(host extension.CustomHost, theme extension.Theme, keybindings extension.KeybindingsManager, done func(any)) (extension.Component, error) {
		view := NewMcpManagerView(host, themeOf(theme), keybindingsOf(keybindings))
		running.Go(func() {
			if err := manage(view); err != nil {
				events.notify(err.Error(), "error")
			}
			done(nil)
		})
		return view, nil
	}), nil)
	return err
}

func keybindingsOf(keybindings extension.KeybindingsManager) *tui.TUIKeybindingsManager {
	if kb, ok := keybindings.(*tui.TUIKeybindingsManager); ok && kb != nil {
		return kb
	}
	return tui.GetTUIKeybindings()
}
