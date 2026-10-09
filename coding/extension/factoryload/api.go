package factoryload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type loadState int

const (
	stateLoading loadState = iota
	stateActive
	stateFailed
)

// api is the registering [extension.API] of one extension: it writes registrations to the extension record and sends the runtime's
// actions through the runtime the runner binds. Until the factory returns, runtime changes are pending.
//
// upstream: loader.ts:244-548 (createExtensionAPI)
type api struct {
	ext     *extension.Extension
	runtime *extension.ExtensionRuntime
	cwd     string
	bus     extension.EventBus

	mu                 sync.Mutex
	state              loadState
	nextHandler        int
	pendingFlagValues  map[string]any
	pendingFlagOrder   []string
	pendingChanges     []func()
	loadingUnsubscribe []func()
	subscriptions      []func()
	retired            string
}

func newAPI(ext *extension.Extension, runtime *extension.ExtensionRuntime, cwd string, bus extension.EventBus) *api {
	return &api{ext: ext, runtime: runtime, cwd: cwd, bus: bus, pendingFlagValues: map[string]any{}}
}

var _ extension.API = (*api)(nil)

// assertActive panics, as upstream throws, when the API's load failed or its runtime is stale.
// upstream: loader.ts:252-257
func (a *api) assertActive() {
	a.mu.Lock()
	failed, retired := a.state == stateFailed, a.retired
	a.mu.Unlock()
	if retired != "" {
		panic(errors.New(retired))
	}
	if failed {
		panic(fmt.Errorf(`Extension "%s" failed to load and its API is no longer active.`, a.ext.Path))
	}
	if err := a.runtime.AssertActive(); err != nil {
		panic(err)
	}
}

// applyRuntimeChange queues change while the extension loads and applies it at once afterwards.
// upstream: loader.ts:258-261
func (a *api) applyRuntimeChange(change func()) {
	a.mu.Lock()
	if a.state == stateLoading {
		a.pendingChanges = append(a.pendingChanges, change)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	change()
}

// commit applies the pending flag defaults and runtime changes and marks the API active.
// upstream: loader.ts:534-543
func (a *api) commit() error {
	a.mu.Lock()
	if a.state != stateLoading {
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	if err := a.runtime.AssertActive(); err != nil {
		return err
	}
	a.mu.Lock()
	order, values, changes := a.pendingFlagOrder, a.pendingFlagValues, a.pendingChanges
	a.state = stateActive
	a.clearPendingLocked()
	a.mu.Unlock()
	for _, name := range order {
		a.runtime.SetFlagDefault(name, values[name])
	}
	for _, apply := range changes {
		apply()
	}
	return nil
}

// retire makes the API stale after a reload loaded the same extension again: its calls fail with message and its event-bus subscriptions end.
func (a *api) retire(message string) {
	a.mu.Lock()
	a.retired = message
	subscriptions := a.subscriptions
	a.subscriptions = nil
	a.mu.Unlock()
	for _, unsubscribe := range subscriptions {
		unsubscribe()
	}
}

// discard marks the API failed, drops the subscriptions made while loading and forgets the pending changes.
// upstream: loader.ts:544-549
func (a *api) discard() {
	a.mu.Lock()
	if a.state != stateLoading {
		a.mu.Unlock()
		return
	}
	a.state = stateFailed
	unsubscribe := a.loadingUnsubscribe
	a.clearPendingLocked()
	a.mu.Unlock()
	for _, f := range unsubscribe {
		f()
	}
}

func (a *api) clearPendingLocked() {
	a.pendingFlagValues = map[string]any{}
	a.pendingFlagOrder = nil
	a.pendingChanges = nil
	a.loadingUnsubscribe = nil
}

// notInitialized is the error of an action called before the runner binds its core.
// upstream: loader.ts:157-160
func notInitialized() error {
	return errors.New("Extension runtime not initialized. Action methods cannot be called during extension loading.")
}

func (a *api) RegisterTool(tool extension.ToolDefinition) {
	a.assertActive()
	var parameters any
	if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
		parameters = nil
	}
	if _, ok := parameters.(map[string]any); !ok {
		panic(fmt.Errorf(`Tool "%s" registered by extension "%s" must define an object parameter schema.`, tool.Name, a.ext.Path))
	}
	a.ext.SetRegisteredTool(extension.RegisteredTool{Definition: tool, SourceInfo: a.ext.SourceInfo})
	// registerTool() is valid during extension load; refresh is only needed after the runner binds.
	if refresh := a.runtime.RefreshTools; refresh != nil {
		if err := refresh(); err != nil {
			panic(err)
		}
	}
}

func (a *api) RegisterCommand(name string, options extension.CommandOptions) {
	a.assertActive()
	if name == "" {
		panic(fmt.Errorf(`Command registered by extension "%s" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).`, a.ext.Path))
	}
	if options.Handler == nil {
		panic(fmt.Errorf(`Command "/%s" registered by extension "%s" must define handler().`, name, a.ext.Path))
	}
	if _, exists := a.ext.Commands[name]; !exists {
		a.ext.CommandOrder = append(a.ext.CommandOrder, name)
	}
	a.ext.Commands[name] = extension.RegisteredCommand{Name: name, SourceInfo: a.ext.SourceInfo, Description: options.Description, GetArgumentCompletions: options.GetArgumentCompletions, Handler: options.Handler}
}

func (a *api) RegisterShortcut(shortcut extension.KeyID, options extension.ShortcutOptions) {
	a.assertActive()
	if a.ext.Shortcuts == nil {
		a.ext.Shortcuts = map[extension.KeyID]extension.ExtensionShortcut{}
	}
	a.ext.Shortcuts[shortcut] = extension.ExtensionShortcut{Shortcut: shortcut, Description: options.Description, Handler: options.Handler, ExtensionPath: a.ext.Path}
}

func (a *api) RegisterFlag(name string, options extension.FlagOptions) {
	a.assertActive()
	if options.Default != nil && !flagDefaultMatches(options) {
		panic(fmt.Errorf(`Invalid default for flag "%s": expected %s, got %s`, name, options.Type, jsTypeof(options.Default)))
	}
	if _, exists := a.ext.Flags[name]; !exists {
		a.ext.FlagOrder = append(a.ext.FlagOrder, name)
	}
	a.ext.Flags[name] = extension.ExtensionFlag{Name: name, Description: options.Description, Type: options.Type, Default: options.Default, ExtensionPath: a.ext.Path}
	if options.Default == nil {
		return
	}
	if _, has := a.runtime.FlagValue(name); has {
		return
	}
	a.mu.Lock()
	if a.state == stateLoading {
		if _, pending := a.pendingFlagValues[name]; !pending {
			a.pendingFlagValues[name] = options.Default
			a.pendingFlagOrder = append(a.pendingFlagOrder, name)
		}
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.runtime.SetFlagDefault(name, options.Default)
}

func flagDefaultMatches(options extension.FlagOptions) bool {
	switch options.Default.(type) {
	case bool:
		return options.Type == extension.FlagBoolean
	case string:
		return options.Type == extension.FlagString
	}
	return false
}

// jsTypeof is JavaScript's typeof for the values a flag default holds.
func jsTypeof(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case string:
		return "string"
	case nil:
		return "undefined"
	}
	return "number"
}

func (a *api) GetFlag(name string) any {
	a.assertActive()
	if _, registered := a.ext.Flags[name]; !registered {
		return nil
	}
	if value, has := a.runtime.FlagValue(name); has {
		return value
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingFlagValues[name]
}

func (a *api) RegisterMessageRenderer(customType string, renderer extension.MessageRenderer) {
	a.assertActive()
	if a.ext.MessageRenderers == nil {
		a.ext.MessageRenderers = map[string]extension.MessageRenderer{}
	}
	a.ext.MessageRenderers[customType] = renderer
}

func (a *api) RegisterEntryRenderer(customType string, renderer extension.EntryRenderer) {
	a.assertActive()
	if a.ext.EntryRenderers == nil {
		a.ext.EntryRenderers = map[string]extension.EntryRenderer{}
	}
	a.ext.EntryRenderers[customType] = renderer
}

func (a *api) RegisterToolRenderer(resolver extension.ToolRendererResolver) {
	a.assertActive()
	a.ext.ToolRenderers = append(a.ext.ToolRenderers, resolver)
}

func (a *api) RegisterMarkdownTransformer(transformer extension.MarkdownTransformer) {
	a.assertActive()
	a.ext.MarkdownTransformer = transformer
}

func (a *api) SendMessage(message extension.SendMessagePayload, options *extension.SendMessageOptions) {
	a.assertActive()
	send := a.runtime.SendMessage
	if send == nil {
		panic(notInitialized())
	}
	if err := send(extension.CustomMessageRef(message), options); err != nil {
		panic(err)
	}
}

func (a *api) SendUserMessage(content any, options *extension.SendUserMessageOptions) {
	a.assertActive()
	send := a.runtime.SendUserMessage
	if send == nil {
		panic(notInitialized())
	}
	if err := send(content, options); err != nil {
		panic(err)
	}
}

func (a *api) AppendEntry(customType string, data any) {
	a.assertActive()
	appendEntry := a.runtime.AppendEntry
	if appendEntry == nil {
		panic(notInitialized())
	}
	if err := appendEntry(customType, data); err != nil {
		panic(err)
	}
}

func (a *api) SetSessionName(name string) {
	a.assertActive()
	set := a.runtime.SetSessionName
	if set == nil {
		panic(notInitialized())
	}
	if err := set(name); err != nil {
		panic(err)
	}
}

func (a *api) GetSessionName() string {
	a.assertActive()
	get := a.runtime.GetSessionName
	if get == nil {
		panic(notInitialized())
	}
	return get()
}

// SetLabel sets the label of an entry; an empty label clears it, as upstream's `undefined` does.
func (a *api) SetLabel(entryID string, label string) {
	a.assertActive()
	set := a.runtime.SetLabel
	if set == nil {
		panic(notInitialized())
	}
	var value *string
	if label != "" {
		value = &label
	}
	if err := set(entryID, value); err != nil {
		panic(err)
	}
}

func (a *api) Exec(command string, args []string, options *extension.ExecOptions) (extension.ExecResult, error) {
	a.assertActive()
	return extension.ExecCommand(context.Background(), a.cwd, command, args, options)
}

func (a *api) GetActiveTools() []string {
	a.assertActive()
	get := a.runtime.GetActiveTools
	if get == nil {
		panic(notInitialized())
	}
	return get()
}

func (a *api) GetAllTools() []extension.ToolInfo {
	a.assertActive()
	get := a.runtime.GetAllTools
	if get == nil {
		panic(notInitialized())
	}
	return get()
}

func (a *api) GetSettings() extension.Settings {
	a.assertActive()
	get := a.runtime.GetSettings
	if get == nil {
		panic(notInitialized())
	}
	return get()
}

func (a *api) SetActiveTools(toolNames []string) {
	a.assertActive()
	set := a.runtime.SetActiveTools
	if set == nil {
		panic(notInitialized())
	}
	set(toolNames)
}

func (a *api) GetCommands() []extension.SlashCommandInfo {
	a.assertActive()
	get := a.runtime.GetCommands
	if get == nil {
		panic(notInitialized())
	}
	return get()
}

func (a *api) SetModel(model extension.Model) (bool, error) {
	a.assertActive()
	set := a.runtime.SetModel
	if set == nil {
		return false, errors.New("Extension runtime not initialized")
	}
	return set(context.Background(), model)
}

func (a *api) GetThinkingLevel() extension.ThinkingLevel {
	a.assertActive()
	get := a.runtime.GetThinkingLevel
	if get == nil {
		panic(notInitialized())
	}
	return get()
}

func (a *api) SetThinkingLevel(level extension.ThinkingLevel) {
	a.assertActive()
	set := a.runtime.SetThinkingLevel
	if set == nil {
		panic(notInitialized())
	}
	set(level)
}

func (a *api) RegisterProvider(name string, config extension.ProviderConfig) {
	a.assertActive()
	a.applyRuntimeChange(func() {
		if err := a.runtime.RegisterProvider(name, config, a.ext.Path); err != nil {
			panic(err)
		}
	})
}

func (a *api) RegisterNativeProvider(provider *ai.ModelsProvider) {
	a.assertActive()
	a.applyRuntimeChange(func() {
		if err := a.runtime.RegisterNativeProvider(context.Background(), provider, a.ext.Path); err != nil {
			panic(err)
		}
	})
}

func (a *api) UnregisterProvider(name string) {
	a.assertActive()
	a.applyRuntimeChange(func() { a.runtime.UnregisterProvider(name, a.ext.Path) })
}

func (a *api) RegisterMcpServer(name string, config extension.McpServerConfig) error {
	a.assertActive()
	raw, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf(`Invalid MCP server registered by extension "%s": %w`, a.ext.Path, err)
	}
	if _, err := a.runtime.CheckMcpServer(a.ext.Path, name, raw); err != nil {
		return err
	}
	// Names that differ only in `-` and `_` would share a namespace.
	for _, server := range a.runtime.McpServers().List() {
		if server.Name != name && extension.McpNamespace(server.Name) == extension.McpNamespace(name) {
			return fmt.Errorf(`MCP server "%s" conflicts with registered server "%s"`, name, server.Name)
		}
	}
	a.applyRuntimeChange(func() {
		if err := a.runtime.RegisterMcpServer(a.ext.Path, name, raw); err != nil {
			panic(err)
		}
	})
	return nil
}

func (a *api) UnregisterMcpServer(name string) {
	a.assertActive()
	a.applyRuntimeChange(func() { a.runtime.UnregisterMcpServer(a.ext.Path, name) })
}

func (a *api) GetMcpServers() []extension.RegisteredMcpServer {
	a.assertActive()
	return a.runtime.McpServers().List()
}

func (a *api) RegisterVirtualModel(model extension.ExtensionVirtualModel) {
	a.assertActive()
	a.applyRuntimeChange(func() {
		if err := a.runtime.RegisterVirtualModel(model, a.ext.Path); err != nil {
			panic(err)
		}
	})
}

func (a *api) UnregisterVirtualModel(provider, id string) {
	a.assertActive()
	a.applyRuntimeChange(func() { a.runtime.UnregisterVirtualModel(provider, id) })
}

func (a *api) Events() extension.EventBus { return apiEvents{a} }

// apiEvents is the API's event bus: calls fail once the API is inactive, and a subscription lasts until its runtime is invalidated.
// upstream: loader.ts:513-532 (events)
type apiEvents struct{ a *api }

func (e apiEvents) Emit(channel string, data any) {
	e.a.assertActive()
	e.a.bus.Emit(channel, data)
}

func (e apiEvents) On(channel string, handler func(data any)) func() {
	e.a.assertActive()
	unsubscribe := e.a.runtime.TrackEventBusSubscription(e.a.bus.On(channel, handler))
	e.a.mu.Lock()
	e.a.subscriptions = append(e.a.subscriptions, unsubscribe)
	if e.a.state == stateLoading {
		e.a.loadingUnsubscribe = append(e.a.loadingUnsubscribe, unsubscribe)
	}
	e.a.mu.Unlock()
	return unsubscribe
}
