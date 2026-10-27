package sdk

import (
	"context"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// RegisteredToolDefinition is the data members of the ToolDefinition an extension registered (Pi's `RegisteredTool["definition"]`); its functions (execute, the renderers) live in the extension that registered it.
type RegisteredToolDefinition struct {
	Name             string          `json:"name"`
	Label            string          `json:"label"`
	Description      string          `json:"description"`
	PromptSnippet    string          `json:"promptSnippet,omitempty"`
	PromptGuidelines []string        `json:"promptGuidelines,omitempty"`
	Parameters       json.RawMessage `json:"parameters"`
	ExecutionMode    string          `json:"executionMode,omitempty"`
	DefaultActive    *bool           `json:"defaultActive,omitempty"`
	Exposure         string          `json:"exposure,omitempty"`
}

// RegisteredTool is Pi's RegisteredTool: a tool definition and where it came from.
type RegisteredTool struct {
	Definition RegisteredToolDefinition `json:"definition"`
	SourceInfo SourceInfo               `json:"sourceInfo"`
}

// ExtensionFlag is Pi's ExtensionFlag: a flag an extension registered. Type is "boolean" or "string".
type ExtensionFlag struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Type          string `json:"type"`
	Default       any    `json:"default,omitempty"`
	ExtensionPath string `json:"extensionPath"`
}

// ExtensionShortcut is the data of Pi's ExtensionShortcut: a shortcut an extension registered.
type ExtensionShortcut struct {
	Shortcut      string `json:"shortcut"`
	Description   string `json:"description,omitempty"`
	ExtensionPath string `json:"extensionPath"`
}

// ShortcutEntry is one entry of the Map getShortcuts returns: a normalized key and its shortcut.
type ShortcutEntry struct {
	Key      string
	Shortcut ExtensionShortcut
}

// ResolvedCommand is Pi's ResolvedCommand: a command and the name it is invoked under, which differs from its own when two extensions register the same one.
type ResolvedCommand struct {
	Name           string     `json:"name"`
	SourceInfo     SourceInfo `json:"sourceInfo"`
	Description    string     `json:"description,omitempty"`
	InvocationName string     `json:"invocationName"`
}

// ResolvedToolRenderers is the shape of Pi's ToolRenderers a runner can hand over: which renderers exist. The renderer functions run in the extension that registered them.
type ResolvedToolRenderers struct {
	RenderCall   bool   `json:"renderCall"`
	RenderResult bool   `json:"renderResult"`
	RenderShell  string `json:"renderShell,omitempty"`
}

// GetExtensionPaths is `getExtensionPaths()`: the path of each extension, in load order.
func (r *ExtensionRunner) GetExtensionPaths() ([]string, error) {
	return graphMethodCall[[]string](r.object, "getExtensionPaths")
}

// GetAllRegisteredTools is `getAllRegisteredTools()`: each extension's tools in load order, the first registration of a name winning.
func (r *ExtensionRunner) GetAllRegisteredTools() ([]RegisteredTool, error) {
	return graphMethodCall[[]RegisteredTool](r.object, "getAllRegisteredTools")
}

// GetToolDefinition is `getToolDefinition(toolName)`; nil is Pi's undefined.
func (r *ExtensionRunner) GetToolDefinition(toolName string) (*RegisteredToolDefinition, error) {
	return graphMethodCall[*RegisteredToolDefinition](r.object, "getToolDefinition", toolName)
}

// GetFlags is `getFlags()`: the flags the extensions registered, the first registration of a name winning.
func (r *ExtensionRunner) GetFlags() (map[string]ExtensionFlag, error) {
	return graphMethodCall[map[string]ExtensionFlag](r.object, "getFlags")
}

// SetFlagValue is `setFlagValue(name, value)`: value is a bool or a string.
func (r *ExtensionRunner) SetFlagValue(name string, value any) error {
	_, err := graphMethodCall[graphDone](r.object, "setFlagValue", name, value)
	return err
}

// GetFlagValues is `getFlagValues()`: a copy of the runtime's flag values, listed by name.
func (r *ExtensionRunner) GetFlagValues() (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getFlagValues")
}

// GetShortcuts is `getShortcuts(resolvedKeybindings)`: the extensions' shortcuts under their normalized keys, in Pi's Map order. resolvedKeybindings is the resolved keybinding table: each action's keys. A conflict with a built-in or another extension's shortcut is reported by [ExtensionRunner.GetShortcutDiagnostics].
func (r *ExtensionRunner) GetShortcuts(resolvedKeybindings map[string][]string) ([]ShortcutEntry, error) {
	entries, err := graphMethodCall[[][2]json.RawMessage](r.object, "getShortcuts", resolvedKeybindings)
	if err != nil {
		return nil, err
	}
	out := make([]ShortcutEntry, len(entries))
	for i, entry := range entries {
		if err := json.Unmarshal(entry[0], &out[i].Key); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(entry[1], &out[i].Shortcut); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetShortcutDiagnostics is `getShortcutDiagnostics()`: the conflicts the last getShortcuts found.
func (r *ExtensionRunner) GetShortcutDiagnostics() ([]ResourceDiagnostic, error) {
	return graphMethodCall[[]ResourceDiagnostic](r.object, "getShortcutDiagnostics")
}

// HasHandlers is `hasHandlers(eventType)`.
func (r *ExtensionRunner) HasHandlers(eventType string) (bool, error) {
	return graphMethodCall[bool](r.object, "hasHandlers", eventType)
}

// ExtensionRenderer is a renderer an extension registered (a message renderer, an entry renderer or a Markdown transformer). The function runs in the extension; the value says that one exists, as Pi's lookup returns a function or undefined.
type ExtensionRenderer struct{}

// GetMessageRenderer is `getMessageRenderer(customType)`; nil is Pi's undefined.
func (r *ExtensionRunner) GetMessageRenderer(customType string) (*ExtensionRenderer, error) {
	found, err := graphMethodCall[bool](r.object, "getMessageRenderer", customType)
	if err != nil || !found {
		return nil, err
	}
	return &ExtensionRenderer{}, nil
}

// GetEntryRenderer is `getEntryRenderer(customType)`; nil is Pi's undefined.
func (r *ExtensionRunner) GetEntryRenderer(customType string) (*ExtensionRenderer, error) {
	found, err := graphMethodCall[bool](r.object, "getEntryRenderer", customType)
	if err != nil || !found {
		return nil, err
	}
	return &ExtensionRenderer{}, nil
}

// GetMarkdownTransformers is `getMarkdownTransformers()`: one entry per extension that registered a transformer, in load order.
func (r *ExtensionRunner) GetMarkdownTransformers() ([]ExtensionRenderer, error) {
	count, err := graphMethodCall[int](r.object, "getMarkdownTransformers")
	if err != nil {
		return nil, err
	}
	return make([]ExtensionRenderer, count), nil
}

// ResolveToolRenderers is `resolveToolRenderers(toolName, base)`: the renderers of calls to toolName, the extensions' resolvers in load order and then base, a function of this process that answers the renderers to use when no extension chose; nil is Pi's undefined. The result is nil when no renderers apply.
func (r *ExtensionRunner) ResolveToolRenderers(toolName string, base func() *ResolvedToolRenderers) (*ResolvedToolRenderers, error) {
	var id any
	if base != nil {
		id = r.object.context.callbackID(func([]json.RawMessage) (any, error) { return base(), nil })
	}
	return graphMethodCall[*ResolvedToolRenderers](r.object, "resolveToolRenderers", toolName, id)
}

// GetModelRegistry is `getModelRegistry()`: the registry the runner was built with.
func (r *ExtensionRunner) GetModelRegistry() *ModelRuntime { return r.modelRegistry }

// GetRegisteredCommands is `getRegisteredCommands()`: each extension's commands in load order, a name two extensions registered invoked as `name:1` and `name:2`.
func (r *ExtensionRunner) GetRegisteredCommands() ([]ResolvedCommand, error) {
	return graphMethodCall[[]ResolvedCommand](r.object, "getRegisteredCommands")
}

// GetCommandDiagnostics is `getCommandDiagnostics()`.
func (r *ExtensionRunner) GetCommandDiagnostics() ([]ResourceDiagnostic, error) {
	return graphMethodCall[[]ResourceDiagnostic](r.object, "getCommandDiagnostics")
}

// GetCommand is `getCommand(name)`: the command invoked under name; nil is Pi's undefined.
func (r *ExtensionRunner) GetCommand(name string) (*ResolvedCommand, error) {
	return graphMethodCall[*ResolvedCommand](r.object, "getCommand", name)
}

// Shutdown is `shutdown()`: it calls the shutdown function bound by [ExtensionRunner.BindCore].
func (r *ExtensionRunner) Shutdown() error {
	_, err := graphMethodCall[graphDone](r.object, "shutdown")
	return err
}

// GetActiveTools is `getActiveTools()`: the active tool names the bound action answers. It fails before the runner is bound, and after it is invalidated.
func (r *ExtensionRunner) GetActiveTools() ([]string, error) {
	return graphMethodCall[[]string](r.object, "getActiveTools")
}

// Invalidate is `invalidate(message?)`: later uses of the runner's contexts fail with message, or Pi's default one when it is nil. A second call keeps the first message.
func (r *ExtensionRunner) Invalidate(message *string) error {
	_, err := graphMethodCall[graphDone](r.object, "invalidate", message)
	return err
}

// ReportUnhandledMcpServers is `reportUnhandledMcpServers()`: it reports through the error listeners each registered MCP server that no loaded extension handles.
func (r *ExtensionRunner) ReportUnhandledMcpServers() error {
	_, err := graphMethodCall[graphDone](r.object, "reportUnhandledMcpServers")
	return err
}

// HasUI is `hasUI()`: whether a UI context is set.
func (r *ExtensionRunner) HasUI() (bool, error) {
	return graphMethodCall[bool](r.object, "hasUI")
}

// ExtensionUIContext is Pi's ExtensionUIContext as the functions of this process that [ExtensionRunner.SetUIContext] installs. A nil function is Pi's no-op one. The members that take or return TUI components (setFooter, setHeader, custom, setEditorComponent, addAutocompleteProvider, theme and the theme functions, onTerminalInput) hold objects of the process that draws them and are not served.
type ExtensionUIContext struct {
	Select                 func(title string, options []string, opts *ExtensionUIDialogOptions) (*string, error)
	Confirm                func(title, message string, opts *ExtensionUIDialogOptions) (bool, error)
	Input                  func(title string, placeholder *string, opts *ExtensionUIDialogOptions) (*string, error)
	Notify                 func(message string, kind *string)
	SetStatus              func(key string, text *string)
	SetWorkingMessage      func(message *string)
	SetWorkingVisible      func(visible bool)
	SetHiddenThinkingLabel func(label *string)
	SetWidget              func(key string, content []string, options *ExtensionWidgetOptions)
	SetTitle               func(title string)
	PasteToEditor          func(text string)
	SetEditorText          func(text string)
	GetEditorText          func() string
	Editor                 func(title string, prefill *string) (*string, error)
	GetToolsExpanded       func() bool
	SetToolsExpanded       func(expanded bool)
}

// ExtensionUIDialogOptions is Pi's ExtensionUIDialogOptions.
type ExtensionUIDialogOptions struct {
	Timeout *float64 `json:"timeout,omitempty"`
}

// ExtensionWidgetOptions is Pi's ExtensionWidgetOptions: Placement is "aboveEditor" or "belowEditor".
type ExtensionWidgetOptions struct {
	Placement string `json:"placement,omitempty"`
}

// SetUIContext is `setUIContext(uiContext?, mode = "print")`. A nil uiContext is Pi's undefined: no UI. mode is "tui", "rpc", "json" or "print"; empty is "print". The runner wraps the dialogs of the UI so each reports ui_prompt_start and ui_prompt_end to the extensions.
func (r *ExtensionRunner) SetUIContext(ui *ExtensionUIContext, mode string) error {
	wire := any(nil)
	if ui != nil {
		wire = r.uiWire(ui)
	}
	if _, err := graphMethodCall[graphDone](r.object, "setUIContext", wire, mode); err != nil {
		return err
	}
	return nil
}

func (r *ExtensionRunner) uiWire(ui *ExtensionUIContext) map[string]any {
	c := r.object.context
	wire := map[string]any{}
	named := func(name string, callback graphCallback) { wire[name] = c.callbackID(callback) }
	if f := ui.Select; f != nil {
		named("select", func(args []json.RawMessage) (any, error) {
			title, _ := graphArg[string](args, 0)
			options, _ := graphArg[[]string](args, 1)
			opts, _ := graphArg[*ExtensionUIDialogOptions](args, 2)
			return f(title, options, opts)
		})
	}
	if f := ui.Confirm; f != nil {
		named("confirm", func(args []json.RawMessage) (any, error) {
			title, _ := graphArg[string](args, 0)
			message, _ := graphArg[string](args, 1)
			opts, _ := graphArg[*ExtensionUIDialogOptions](args, 2)
			return f(title, message, opts)
		})
	}
	if f := ui.Input; f != nil {
		named("input", func(args []json.RawMessage) (any, error) {
			title, _ := graphArg[string](args, 0)
			placeholder, _ := graphArg[*string](args, 1)
			opts, _ := graphArg[*ExtensionUIDialogOptions](args, 2)
			return f(title, placeholder, opts)
		})
	}
	if f := ui.Notify; f != nil {
		named("notify", func(args []json.RawMessage) (any, error) {
			message, _ := graphArg[string](args, 0)
			kind, _ := graphArg[*string](args, 1)
			f(message, kind)
			return nil, nil
		})
	}
	if f := ui.SetStatus; f != nil {
		named("setStatus", func(args []json.RawMessage) (any, error) {
			key, _ := graphArg[string](args, 0)
			text, _ := graphArg[*string](args, 1)
			f(key, text)
			return nil, nil
		})
	}
	if f := ui.SetWorkingMessage; f != nil {
		named("setWorkingMessage", func(args []json.RawMessage) (any, error) {
			message, _ := graphArg[*string](args, 0)
			f(message)
			return nil, nil
		})
	}
	if f := ui.SetWorkingVisible; f != nil {
		named("setWorkingVisible", func(args []json.RawMessage) (any, error) {
			visible, _ := graphArg[bool](args, 0)
			f(visible)
			return nil, nil
		})
	}
	if f := ui.SetHiddenThinkingLabel; f != nil {
		named("setHiddenThinkingLabel", func(args []json.RawMessage) (any, error) {
			label, _ := graphArg[*string](args, 0)
			f(label)
			return nil, nil
		})
	}
	if f := ui.SetWidget; f != nil {
		named("setWidget", func(args []json.RawMessage) (any, error) {
			key, _ := graphArg[string](args, 0)
			content, _ := graphArg[[]string](args, 1)
			options, _ := graphArg[*ExtensionWidgetOptions](args, 2)
			f(key, content, options)
			return nil, nil
		})
	}
	if f := ui.SetTitle; f != nil {
		named("setTitle", func(args []json.RawMessage) (any, error) {
			title, _ := graphArg[string](args, 0)
			f(title)
			return nil, nil
		})
	}
	if f := ui.PasteToEditor; f != nil {
		named("pasteToEditor", func(args []json.RawMessage) (any, error) {
			text, _ := graphArg[string](args, 0)
			f(text)
			return nil, nil
		})
	}
	if f := ui.SetEditorText; f != nil {
		named("setEditorText", func(args []json.RawMessage) (any, error) {
			text, _ := graphArg[string](args, 0)
			f(text)
			return nil, nil
		})
	}
	if f := ui.GetEditorText; f != nil {
		named("getEditorText", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := ui.Editor; f != nil {
		named("editor", func(args []json.RawMessage) (any, error) {
			title, _ := graphArg[string](args, 0)
			prefill, _ := graphArg[*string](args, 1)
			return f(title, prefill)
		})
	}
	if f := ui.GetToolsExpanded; f != nil {
		named("getToolsExpanded", func([]json.RawMessage) (any, error) { return f(), nil })
	}
	if f := ui.SetToolsExpanded; f != nil {
		named("setToolsExpanded", func(args []json.RawMessage) (any, error) {
			expanded, _ := graphArg[bool](args, 0)
			f(expanded)
			return nil, nil
		})
	}
	return wire
}

// RunnerUIContext is the runner's UI context, Pi's `runner.getUIContext()`: the calls go through the runner, which wraps the dialogs of the context [ExtensionRunner.SetUIContext] installed (or Pi's no-op context) so they report ui_prompt_start and ui_prompt_end.
type RunnerUIContext struct{ runner *ExtensionRunner }

// GetUIContext is `getUIContext()`.
func (r *ExtensionRunner) GetUIContext() *RunnerUIContext { return &RunnerUIContext{runner: r} }

func (u *RunnerUIContext) call(member string, args ...any) error {
	_, err := graphMethodAsync[graphDone](u.runner.object, "ui", append([]any{member}, args...)...)
	return err
}

// Select is `ui.select(title, options, opts?)`; nil is the dialog's undefined (cancelled).
func (u *RunnerUIContext) Select(title string, options []string, opts *ExtensionUIDialogOptions) (*string, error) {
	return graphMethodAsync[*string](u.runner.object, "ui", "select", title, options, opts)
}

// Confirm is `ui.confirm(title, message, opts?)`.
func (u *RunnerUIContext) Confirm(title, message string, opts *ExtensionUIDialogOptions) (bool, error) {
	return graphMethodAsync[bool](u.runner.object, "ui", "confirm", title, message, opts)
}

// Input is `ui.input(title, placeholder?, opts?)`; nil is the dialog's undefined.
func (u *RunnerUIContext) Input(title string, placeholder *string, opts *ExtensionUIDialogOptions) (*string, error) {
	return graphMethodAsync[*string](u.runner.object, "ui", "input", title, placeholder, opts)
}

// Editor is `ui.editor(title, prefill?)`; nil is the dialog's undefined.
func (u *RunnerUIContext) Editor(title string, prefill *string) (*string, error) {
	return graphMethodAsync[*string](u.runner.object, "ui", "editor", title, prefill)
}

// Notify is `ui.notify(message, type?)`.
func (u *RunnerUIContext) Notify(message string, kind *string) error {
	return u.call("notify", message, kind)
}

// SetStatus is `ui.setStatus(key, text)`.
func (u *RunnerUIContext) SetStatus(key string, text *string) error {
	return u.call("setStatus", key, text)
}

// SetWorkingMessage is `ui.setWorkingMessage(message?)`.
func (u *RunnerUIContext) SetWorkingMessage(message *string) error {
	return u.call("setWorkingMessage", message)
}

// SetWorkingVisible is `ui.setWorkingVisible(visible)`.
func (u *RunnerUIContext) SetWorkingVisible(visible bool) error {
	return u.call("setWorkingVisible", visible)
}

// SetHiddenThinkingLabel is `ui.setHiddenThinkingLabel(label?)`.
func (u *RunnerUIContext) SetHiddenThinkingLabel(label *string) error {
	return u.call("setHiddenThinkingLabel", label)
}

// SetWidget is `ui.setWidget(key, content, options?)` for lines of text.
func (u *RunnerUIContext) SetWidget(key string, content []string, options *ExtensionWidgetOptions) error {
	return u.call("setWidget", key, content, options)
}

// SetTitle is `ui.setTitle(title)`.
func (u *RunnerUIContext) SetTitle(title string) error { return u.call("setTitle", title) }

// PasteToEditor is `ui.pasteToEditor(text)`.
func (u *RunnerUIContext) PasteToEditor(text string) error { return u.call("pasteToEditor", text) }

// SetEditorText is `ui.setEditorText(text)`.
func (u *RunnerUIContext) SetEditorText(text string) error { return u.call("setEditorText", text) }

// GetEditorText is `ui.getEditorText()`.
func (u *RunnerUIContext) GetEditorText() (string, error) {
	return graphMethodCall[string](u.runner.object, "ui", "getEditorText")
}

// GetToolsExpanded is `ui.getToolsExpanded()`.
func (u *RunnerUIContext) GetToolsExpanded() (bool, error) {
	return graphMethodCall[bool](u.runner.object, "ui", "getToolsExpanded")
}

// SetToolsExpanded is `ui.setToolsExpanded(expanded)`.
func (u *RunnerUIContext) SetToolsExpanded(expanded bool) error {
	return u.call("setToolsExpanded", expanded)
}

// ExtensionContext is Pi's ExtensionContext as the runner's createContext returns it: its getters read the runner at call time, so a change bound functions answer is seen, and a runner that was invalidated fails each of them with its message.
type ExtensionContext struct {
	runner *ExtensionRunner
	spec   map[string]any
}

// CreateContext is `createContext()`.
func (r *ExtensionRunner) CreateContext() *ExtensionContext {
	return &ExtensionContext{runner: r, spec: map[string]any{"kind": "context"}}
}

func contextGet[T any](c *ExtensionContext, member string, args ...any) (T, error) {
	return graphMethodCall[T](c.runner.object, "context", append([]any{c.spec, member}, args...)...)
}

// UI is `ctx.ui`.
func (c *ExtensionContext) UI() (*RunnerUIContext, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	return c.runner.GetUIContext(), nil
}

func (c *ExtensionContext) assertActive() error {
	_, err := contextGet[graphDone](c, "assertActive")
	return err
}

// Mode is `ctx.mode`: "tui", "rpc", "json" or "print".
func (c *ExtensionContext) Mode() (string, error) { return contextGet[string](c, "mode") }

// HasUI is `ctx.hasUI`.
func (c *ExtensionContext) HasUI() (bool, error) { return contextGet[bool](c, "hasUI") }

// Cwd is `ctx.cwd`.
func (c *ExtensionContext) Cwd() (string, error) { return contextGet[string](c, "cwd") }

// SessionManager is `ctx.sessionManager`: the log the runner was built with, nil when it has none.
func (c *ExtensionContext) SessionManager() (*SessionManager, error) {
	return c.runner.sessionManager, c.assertActive()
}

// ModelRegistry is `ctx.modelRegistry`: the registry the runner was built with, nil when it has none.
func (c *ExtensionContext) ModelRegistry() (*ModelRuntime, error) {
	return c.runner.modelRegistry, c.assertActive()
}

// Model is `ctx.model`: the model object the bound getModel answers; nil is Pi's undefined.
func (c *ExtensionContext) Model() (map[string]any, error) {
	return contextGet[map[string]any](c, "model")
}

// ScopedModels is `ctx.scopedModels`.
func (c *ExtensionContext) ScopedModels() ([]ScopedModel, error) {
	return contextGet[[]ScopedModel](c, "scopedModels")
}

// ThinkingLevel is `ctx.thinkingLevel`: what the bound getThinkingLevel answers.
func (c *ExtensionContext) ThinkingLevel() (string, error) {
	return contextGet[string](c, "thinkingLevel")
}

// IsIdle is `ctx.isIdle()`.
func (c *ExtensionContext) IsIdle() (bool, error) { return contextGet[bool](c, "isIdle") }

// IsProjectTrusted is `ctx.isProjectTrusted()`.
func (c *ExtensionContext) IsProjectTrusted() (bool, error) {
	return contextGet[bool](c, "isProjectTrusted")
}

// Signal is `ctx.signal`: nil is Pi's undefined (no run); otherwise a context that is done when the signal is aborted.
func (c *ExtensionContext) Signal() (context.Context, error) {
	signal, err := contextGet[*struct {
		Aborted bool `json:"aborted"`
	}](c, "signal")
	if err != nil || signal == nil {
		return nil, err
	}
	if !signal.Aborted {
		return context.Background(), nil
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	return done, nil
}

// Abort is `ctx.abort()`.
func (c *ExtensionContext) Abort() error {
	_, err := contextGet[graphDone](c, "abort")
	return err
}

// HasPendingMessages is `ctx.hasPendingMessages()`.
func (c *ExtensionContext) HasPendingMessages() (bool, error) {
	return contextGet[bool](c, "hasPendingMessages")
}

// Shutdown is `ctx.shutdown()`.
func (c *ExtensionContext) Shutdown() error {
	_, err := contextGet[graphDone](c, "shutdown")
	return err
}

// GetContextUsage is `ctx.getContextUsage()`; nil is Pi's undefined.
func (c *ExtensionContext) GetContextUsage() (*ContextUsage, error) {
	return contextGet[*ContextUsage](c, "getContextUsage")
}

// Compact is `ctx.compact(options?)`: it calls the bound compact function with options, here, so its OnComplete and OnError are this process's.
func (c *ExtensionContext) Compact(options *CompactOptions) error {
	if err := c.assertActive(); err != nil {
		return err
	}
	c.runner.mu.Lock()
	compact := c.runner.contextActions.Compact
	c.runner.mu.Unlock()
	if compact != nil {
		compact(options)
	}
	return nil
}

// GetSystemPrompt is `ctx.getSystemPrompt()`.
func (c *ExtensionContext) GetSystemPrompt() (string, error) {
	return contextGet[string](c, "getSystemPrompt")
}

// ExtensionToolContext is Pi's ExtensionToolContext: the context of one tool call, with the callable tools and nested execution.
type ExtensionToolContext struct {
	ExtensionContext
	toolCallID string
	signal     context.Context
}

// CreateToolContext is `createToolContext(toolCallId, signal)`: signal is the default cancellation of nested calls; nil is Pi's undefined.
func (r *ExtensionRunner) CreateToolContext(toolCallID string, signal context.Context) *ExtensionToolContext {
	return &ExtensionToolContext{ExtensionContext: ExtensionContext{runner: r, spec: map[string]any{"kind": "tool", "toolCallId": toolCallID}}, toolCallID: toolCallID, signal: signal}
}

// Tools is `ctx.tools`: the tools a nested call can run.
func (c *ExtensionToolContext) Tools() ([]AgentTool, error) {
	return contextGet[[]AgentTool](&c.ExtensionContext, "tools")
}

// ExecuteTool is `ctx.executeTool(name, args, options?)`: it calls the bound executeTool function, here, with options whose Signal defaults to the one the context was created with. Without a bound function the outcome is Pi's error outcome ("Nested tool calls are not available in this context").
func (c *ExtensionToolContext) ExecuteTool(name string, args any, options ExecuteToolOptions) (AgentToolCallOutcome, error) {
	if err := c.assertActive(); err != nil {
		return AgentToolCallOutcome{}, err
	}
	c.runner.mu.Lock()
	execute := c.runner.contextActions.ExecuteTool
	c.runner.mu.Unlock()
	if options.Signal == nil {
		options.Signal = c.signal
	}
	if execute == nil {
		return AgentToolCallOutcome{
			ToolCall: ToolCall{ID: c.toolCallID + "/0", Name: name, Arguments: map[string]any{}},
			Result:   AgentToolResult{Content: []ToolResultContent{{Type: "text", Text: "Nested tool calls are not available in this context"}}, Details: map[string]any{}},
			IsError:  true,
		}, nil
	}
	return execute(c.toolCallID, name, args, options)
}

// ExtensionCommandContext is Pi's ExtensionCommandContext: the context of a command, which can wait for the agent and replace the session.
type ExtensionCommandContext struct {
	ExtensionContext
}

// CreateCommandContext is `createCommandContext()`.
func (r *ExtensionRunner) CreateCommandContext() *ExtensionCommandContext {
	return &ExtensionCommandContext{ExtensionContext{runner: r, spec: map[string]any{"kind": "command"}}}
}

// GetSystemPromptOptions is `ctx.getSystemPromptOptions()`.
func (c *ExtensionCommandContext) GetSystemPromptOptions() (*SystemPromptOptions, error) {
	return contextGet[*SystemPromptOptions](&c.ExtensionContext, "getSystemPromptOptions")
}

// commandActions is the functions bound by BindCommandContext, after the context is known to be active.
func (c *ExtensionCommandContext) commandActions() (ExtensionCommandContextActions, error) {
	if err := c.assertActive(); err != nil {
		return ExtensionCommandContextActions{}, err
	}
	c.runner.mu.Lock()
	defer c.runner.mu.Unlock()
	if c.runner.commandActions == nil {
		return ExtensionCommandContextActions{}, nil
	}
	return *c.runner.commandActions, nil
}

// WaitForIdle is `ctx.waitForIdle()`.
func (c *ExtensionCommandContext) WaitForIdle() error {
	actions, err := c.commandActions()
	if err != nil || actions.WaitForIdle == nil {
		return err
	}
	return actions.WaitForIdle()
}

// NewSession is `ctx.newSession(options?)`: the bound function receives options as given, with its Setup and WithSession.
func (c *ExtensionCommandContext) NewSession(options *CommandNewSessionOptions) (CancelledResult, error) {
	actions, err := c.commandActions()
	if err != nil || actions.NewSession == nil {
		return CancelledResult{}, err
	}
	return actions.NewSession(options)
}

// Fork is `ctx.fork(entryId, options?)`.
func (c *ExtensionCommandContext) Fork(entryID string, options *ForkOptions) (CancelledResult, error) {
	actions, err := c.commandActions()
	if err != nil || actions.Fork == nil {
		return CancelledResult{}, err
	}
	return actions.Fork(entryID, options)
}

// NavigateTree is `ctx.navigateTree(targetId, options?)`.
func (c *ExtensionCommandContext) NavigateTree(targetID string, options *NavigateTreeOptions) (CancelledResult, error) {
	actions, err := c.commandActions()
	if err != nil || actions.NavigateTree == nil {
		return CancelledResult{}, err
	}
	return actions.NavigateTree(targetID, options)
}

// SwitchSession is `ctx.switchSession(sessionPath, options?)`.
func (c *ExtensionCommandContext) SwitchSession(sessionPath string, options *SwitchSessionOptions) (CancelledResult, error) {
	actions, err := c.commandActions()
	if err != nil || actions.SwitchSession == nil {
		return CancelledResult{}, err
	}
	return actions.SwitchSession(sessionPath, options)
}

// Reload is `ctx.reload()`.
func (c *ExtensionCommandContext) Reload() error {
	actions, err := c.commandActions()
	if err != nil || actions.Reload == nil {
		return err
	}
	return actions.Reload()
}

// Emit is `emit(event)`: event is Pi's event object (a RunnerEmitEvent: session_start, agent_start, message_update and the other events that have no emit method of their own). The result is what the chain answered, which is non-nil for the session_before_* events only.
func (r *ExtensionRunner) Emit(event map[string]any) (json.RawMessage, error) {
	return graphMethodAsync[json.RawMessage](r.object, "emit", event)
}

// EmitToolCall is `emitToolCall(event)`: a tool_call event, chained so each handler sees the input the earlier ones edited; the result is {block, reason} when a handler blocked, nil otherwise.
func (r *ExtensionRunner) EmitToolCall(event map[string]any) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitToolCall", event)
}

// EmitToolResult is `emitToolResult(event)`: a tool_result event whose handlers' edits chain; the result is the edits ({content, details, isError}), nil when none.
func (r *ExtensionRunner) EmitToolResult(event map[string]any) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitToolResult", event)
}

// EmitInput is `emitInput(text, images, source, streamingBehavior?)`: the result is Pi's InputEventResult, {action: "continue"}, {action: "transform", text, images?} or {action: "handled"}.
func (r *ExtensionRunner) EmitInput(text string, images []map[string]any, source string, streamingBehavior *string) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitInput", text, images, source, streamingBehavior)
}

// EmitContext is `emitContext(messages)`: the messages the `context` handlers left.
func (r *ExtensionRunner) EmitContext(messages []map[string]any) ([]map[string]any, error) {
	return graphMethodAsync[[]map[string]any](r.object, "emitContext", messages)
}

// EmitBeforeAgentStart is `emitBeforeAgentStart(trigger, systemPromptOptions)`: trigger is what starts the run, `{messageType: "user", prompt, images?}` or `{messageType: "custom", message}`. The result is {messages, systemPromptOptions}: the handlers' custom messages and the options their system prompt edits left, with the prompt a handler set as forceSystemPrompt.
func (r *ExtensionRunner) EmitBeforeAgentStart(trigger map[string]any, systemPromptOptions *SystemPromptOptions) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitBeforeAgentStart", trigger, systemPromptOptions)
}

// EmitBeforeProviderRequest is `emitBeforeProviderRequest(payload)`: the payload the handlers left.
func (r *ExtensionRunner) EmitBeforeProviderRequest(payload any) (json.RawMessage, error) {
	return graphMethodAsync[json.RawMessage](r.object, "emitBeforeProviderRequest", payload)
}

// EmitBeforeProviderHeaders is `emitBeforeProviderHeaders(headers)`: the headers the handlers left; a nil value deletes a header.
func (r *ExtensionRunner) EmitBeforeProviderHeaders(headers map[string]*string) (map[string]*string, error) {
	return graphMethodAsync[map[string]*string](r.object, "emitBeforeProviderHeaders", headers)
}

// EmitUserBash is `emitUserBash(event)`: the result is a handler's {result}, nil when none.
func (r *ExtensionRunner) EmitUserBash(event map[string]any) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitUserBash", event)
}

// EmitMessageEnd is `emitMessageEnd(event)`: the message a handler replaced the finalized one with, nil when none did.
func (r *ExtensionRunner) EmitMessageEnd(event map[string]any) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitMessageEnd", event)
}

// EmitResourcesDiscover is `emitResourcesDiscover(cwd, reason)`: the result is {skillPaths, promptPaths, themePaths}, each path with the extension that gave it.
func (r *ExtensionRunner) EmitResourcesDiscover(cwd, reason string) (map[string]any, error) {
	return graphMethodAsync[map[string]any](r.object, "emitResourcesDiscover", cwd, reason)
}

// EmitCacheWarmingDecision is `emitCacheWarmingDecision(event)`: the event's own action, "warm" or "stop", unless a handler overrides it; the last override wins.
func (r *ExtensionRunner) EmitCacheWarmingDecision(event map[string]any) (string, error) {
	return graphMethodAsync[string](r.object, "emitCacheWarmingDecision", event)
}

// EmitBoundary is `emitBoundary(baseEvent, buildContext)`: baseEvent is a turn_end or agent_before_settle event without entries, continue and context; buildContext is a function of this process that answers the context preview for a list of drafted entries. The result is {entries, continue, context, valid}.
func (r *ExtensionRunner) EmitBoundary(baseEvent map[string]any, buildContext func(entries []map[string]any) (map[string]any, error)) (map[string]any, error) {
	id := r.object.context.callbackID(func(args []json.RawMessage) (any, error) {
		entries, err := graphArg[[]map[string]any](args, 0)
		if err != nil {
			return nil, err
		}
		return buildContext(entries)
	})
	return graphMethodAsync[map[string]any](r.object, "emitBoundary", baseEvent, id)
}
