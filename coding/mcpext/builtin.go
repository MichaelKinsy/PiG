package mcpext

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// NewBuiltin builds the `builtin:mcp` extension: [Factory] registered on an in-process API, so the extension's event
// handlers, `/mcp` command and tools reach a Session's runner like those of any extension. The loader names it and
// supplies its source.
//
// Ports packages/coding-agent/src/extensions/index.ts (the `mcp` entry of builtInExtensions).
func NewBuiltin(options Options) (extension.Extension, error) {
	api := &builtinAPI{ext: &extension.Extension{Commands: map[string]extension.RegisteredCommand{}}}
	Factory(options)(api)
	// The returned value shares both registries with api.ext, so a tool registered after load reaches the runner's copy.
	api.ext.InitializeEventHandlers()
	api.ext.InitializeToolRegistry()
	return *api.ext, nil
}

// builtinAPI is the [API] of an in-process extension: handlers, commands and tools are recorded on ext, and the calls that
// read or change the Session go through the context of the latest event, which a runner binds to its Session. Upstream's
// `pi` object is bound to the runner the same way, through the runtime the runner binds in bindCore.
type builtinAPI struct {
	ext *extension.Extension

	mu     sync.Mutex
	nextID int
	// context is the context of the latest event. The extension calls the API from goroutines it owns (a connection
	// that settles, a server that changes its tool list), after the handler that started them returned.
	context atomic.Pointer[extension.Context]
}

// subscribe records a handler for event. call receives the event and the handler context; its result is the handler's.
func subscribe[E any](a *builtinAPI, event string, call func(ctx context.Context, evt E) (any, error)) {
	a.mu.Lock()
	a.nextID++
	id := a.nextID
	a.mu.Unlock()
	a.ext.AddEventHandler(event, id, func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("%s handler called with %d arguments, want the event and its context", event, len(args))
		}
		evt, ok := args[0].(E)
		if !ok {
			return nil, fmt.Errorf("%s handler called with event %T", event, args[0])
		}
		ctx, ok := args[1].(context.Context)
		if !ok {
			return nil, fmt.Errorf("%s handler called with context %T", event, args[1])
		}
		if c := extension.FromContext(ctx); c != nil {
			a.context.Store(c)
		}
		return call(ctx, evt)
	})
}

// resultOrNil is a handler result that carries nothing as nil, as a handler that returns `undefined` upstream.
func resultOrNil[R any](result R) any {
	if reflect.ValueOf(&result).Elem().IsZero() {
		return nil
	}
	return &result
}

func (a *builtinAPI) OnSessionStart(handler func(ctx context.Context, evt extension.SessionStartEvent) error) {
	subscribe(a, "session_start", func(ctx context.Context, evt extension.SessionStartEvent) (any, error) {
		return nil, handler(ctx, evt)
	})
}

func (a *builtinAPI) OnBeforeAgentStart(handler func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error)) {
	subscribe(a, "before_agent_start", func(ctx context.Context, evt extension.BeforeAgentStartEvent) (any, error) {
		result, err := handler(ctx, evt)
		return resultOrNil(result), err
	})
}

func (a *builtinAPI) OnToolCall(handler func(ctx context.Context, evt extension.ToolCallEvent) (extension.ToolCallEventResult, error)) {
	subscribe(a, "tool_call", func(ctx context.Context, evt extension.ToolCallEvent) (any, error) {
		result, err := handler(ctx, evt)
		return resultOrNil(result), err
	})
}

func (a *builtinAPI) OnTurnStart(handler func(ctx context.Context, evt extension.TurnStartEvent) error) {
	subscribe(a, "turn_start", func(ctx context.Context, evt extension.TurnStartEvent) (any, error) {
		return nil, handler(ctx, evt)
	})
}

func (a *builtinAPI) OnMcpServersChange(handler func(ctx context.Context, evt extension.McpServersChangeEvent) error) {
	subscribe(a, "mcp_servers_change", func(ctx context.Context, evt extension.McpServersChangeEvent) (any, error) {
		return nil, handler(ctx, evt)
	})
}

func (a *builtinAPI) OnSessionShutdown(handler func(ctx context.Context, evt extension.SessionShutdownEvent) error) {
	subscribe(a, "session_shutdown", func(ctx context.Context, evt extension.SessionShutdownEvent) (any, error) {
		return nil, handler(ctx, evt)
	})
}

// RegisterCommand records the command. The loader gives it its source.
func (a *builtinAPI) RegisterCommand(name string, options extension.CommandOptions) {
	if _, exists := a.ext.Commands[name]; !exists {
		a.ext.CommandOrder = append(a.ext.CommandOrder, name)
	}
	a.ext.Commands[name] = extension.RegisteredCommand{Name: name, Description: options.Description, GetArgumentCompletions: options.GetArgumentCompletions, Handler: options.Handler}
}

// RegisterTool replaces the registration of the tool's name and refreshes the Session's tool registry, as a tool
// registered while a Session runs is admitted and, when it activates on registration, declared. A failure to admit the
// tool is shown to the user, since the caller has no error to return it to.
//
// upstream: core/extensions/loader.ts:273-284 (registerTool)
func (a *builtinAPI) RegisterTool(definition extension.ToolDefinition) {
	a.ext.SetRegisteredTool(extension.RegisteredTool{Definition: definition})
	c := a.context.Load()
	if c == nil {
		return
	}
	if err := c.RefreshTools(); err != nil {
		if ui, uiErr := c.UI(); uiErr == nil {
			ui.Notify(fmt.Sprintf("MCP tool %s could not be registered: %v", definition.Name, err), "error")
		}
	}
}

func (a *builtinAPI) GetAllTools() []extension.ToolInfo {
	if c := a.context.Load(); c != nil {
		return c.GetAllTools()
	}
	return nil
}

func (a *builtinAPI) GetActiveTools() []string {
	if c := a.context.Load(); c != nil {
		return c.GetActiveTools()
	}
	return nil
}

func (a *builtinAPI) SetActiveTools(names []string) {
	if c := a.context.Load(); c != nil {
		c.SetActiveTools(names)
	}
}

func (a *builtinAPI) GetMcpServers() []extension.RegisteredMcpServer {
	if c := a.context.Load(); c != nil {
		return c.GetMcpServers()
	}
	return nil
}
