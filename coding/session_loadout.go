package coding

// Ports the tool loadout members of packages/coding-agent/src/core/agent-session.ts: tool exposure, callable tools, prepareLoadout,
// hidden declarations and the executeTool binding (agent-session.ts:1436-1550, 1683-1706, 3336-3520).

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// sessionLoadoutState is the Session state of tool exposure and nested calls.
type sessionLoadoutState struct {
	// applyMu serializes loadout changes, including the registry refresh that precedes one. It is taken before toolRegistryMu; see [Session.applyToolLoadout].
	applyMu sync.Mutex
	// nested is created on the first `ctx.executeTool()` call. Agent events read it on the agent goroutine while a tool goroutine may create it.
	nestedOnce sync.Once
	nested     atomic.Pointer[NestedToolCallRunner]
	// hidden holds the declared tools whose declarations requests leave out, from `prepareLoadout` hooks.
	hidden atomic.Pointer[map[string]struct{}]
	// emitMu serializes the events of nested calls, which run on tool goroutines.
	emitMu sync.Mutex
	// pending holds the records taken at message_start until message_end applies them to the shared message.
	pendingMu sync.Mutex
	pending   map[string]*NestedCallSummary
}

// toolExposureOf returns the exposure of a definition; the default is `direct`.
//
// upstream: agent-session.ts:1462-1464 (_getToolExposure)
func toolExposureOf(definition extension.ToolDefinition) extension.ToolExposure {
	if definition.Exposure == "" {
		return extension.ToolExposureDirect
	}
	return definition.Exposure
}

// toolIsDeclarable reports whether activating the tool declares it to the model.
//
// upstream: agent-session.ts:3510-3513 (_isDeclarable)
func toolIsDeclarable(definition extension.ToolDefinition) bool {
	exposure := toolExposureOf(definition)
	return exposure == extension.ToolExposureDirect || exposure == extension.ToolExposureModelOnly
}

// toolActivatesOnRegistration reports whether registering the tool activates it, which declares it to the model.
//
// upstream: agent-session.ts:3516-3519 (_isActivatedOnRegistration)
func toolActivatesOnRegistration(definition extension.ToolDefinition) bool {
	return toolIsDeclarable(definition) && (definition.DefaultActive == nil || *definition.DefaultActive)
}

// ExtensionToolStartsActive reports whether an extension tool is active when a Session starts. With an allowlist (`--tools`, `--no-tools`), naming a declarable tool activates it even when it is not active by default; without one, only a tool that activates on registration is active.
//
// upstream: agent-session.ts:3487-3506 (_refreshToolRegistry)
func ExtensionToolStartsActive(definition extension.ToolDefinition, allowed map[string]struct{}) bool {
	if allowed != nil {
		_, named := allowed[definition.Name]
		return named && toolIsDeclarable(definition)
	}
	return toolActivatesOnRegistration(definition)
}

// toolDefinitionLocked returns the admitted definition of name. The caller holds toolRegistryMu.
func (s *Session) toolDefinitionLocked(name string) (extension.ToolDefinition, bool) {
	for _, entry := range s.toolRegistry.entries {
		if entry.tool.Name() == name {
			return entry.registration.Definition, true
		}
	}
	return extension.ToolDefinition{}, false
}

// toolView is the read-only view of a registered tool that loadout hooks and `ctx.tools` see.
func toolView(definition extension.ToolDefinition) extension.AgentTool {
	return extension.AgentTool{Name: definition.Name, Label: definition.Label, Description: definition.Description, Parameters: definition.Parameters, OutputSchema: definition.OutputSchema, ExecutionMode: definition.ExecutionMode}
}

// CallableToolNames returns the names of the tools that tools can call through `ctx.executeTool()`: the active `direct` tools and every registered `codemode` or `deferred` tool, in registry order.
//
// upstream: agent-session.ts:1445-1448 (getCallableToolNames)
func (s *Session) CallableToolNames() []string {
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	var names []string
	for _, entry := range s.callableEntriesLocked(nil) {
		names = append(names, entry.tool.Name())
	}
	return names
}

// callableEntriesLocked is the registry entries of the callable tools: the `direct` tools in active (the current active tools when nil) and every `codemode` or `deferred` tool. The caller holds toolRegistryMu.
//
// upstream: agent-session.ts:1494-1499 (_getCallableTools)
func (s *Session) callableEntriesLocked(active map[string]struct{}) []sessionToolEntry {
	if active == nil {
		active = map[string]struct{}{}
		for _, tool := range s.agent.Tools() {
			active[tool.Name()] = struct{}{}
		}
	}
	var entries []sessionToolEntry
	for _, entry := range s.toolRegistry.entries {
		exposure := toolExposureOf(entry.registration.Definition)
		_, isActive := active[entry.tool.Name()]
		if exposure == extension.ToolExposureCodemode || exposure == extension.ToolExposureDeferred || exposure == extension.ToolExposureDirect && isActive {
			entries = append(entries, entry)
		}
	}
	return entries
}

// applyToolLoadout sets nothing on the agent: it returns the tools declared for the given active names and records the declarations that hooks hide. The active tools are the registered, non-hidden ones; they are declared to the model. Active tools with a prepareLoadout hook can change the declared descriptions and hide declarations from requests (see [Session.hideDeclarations]).
//
// The caller holds loadout.applyMu, then toolRegistryMu. The hooks run with toolRegistryMu released: a hook may read the Session's tools, as a subprocess hook does when the host pushes extension state before it asks, and toolRegistryMu is not reentrant. applyMu keeps other loadout and registry changes out until the loadout is applied.
//
// upstream: agent-session.ts:1500-1560 (_applyToolLoadout)
func (s *Session) applyToolLoadout(names []string) []agent.AgentTool {
	plan := s.planToolLoadout(names)
	var descriptions map[string]string
	hidden := map[string]struct{}{}
	if len(plan.hooks) > 0 {
		s.toolRegistryMu.Unlock()
		descriptions, hidden = s.runLoadoutHooks(plan)
		s.toolRegistryMu.Lock()
	}
	s.loadout.hidden.Store(&hidden)
	result := make([]agent.AgentTool, len(plan.tools))
	for i, tool := range plan.tools {
		if description, ok := descriptions[tool.Name()]; ok {
			result[i] = s.bindTool(tool, description)
		} else {
			result[i] = s.bindTool(tool)
		}
	}
	return result
}

// loadoutHook is an active tool's prepareLoadout hook and the path of the extension that registered it.
type loadoutHook struct {
	prepare    extension.ToolPrepareLoadoutFunc
	sourcePath string
}

// toolLoadoutPlan is the part of a loadout that is read from the tool registry: the active tools, their hooks, and the view the hooks get.
type toolLoadoutPlan struct {
	tools   []agent.AgentTool
	hooks   []loadoutHook
	loadout extension.ToolLoadout
}

// planToolLoadout reads the registry for [Session.applyToolLoadout]. The loadout view is a snapshot, so the hooks can read it without toolRegistryMu. The caller holds toolRegistryMu.
func (s *Session) planToolLoadout(names []string) toolLoadoutPlan {
	registry := make(map[string]agent.AgentTool, len(s.tools))
	for _, tool := range s.tools {
		registry[tool.Name()] = tool
	}
	var plan toolLoadoutPlan
	seen := map[string]struct{}{}
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		tool := registry[name]
		if tool == nil {
			continue
		}
		if definition, ok := s.toolDefinitionLocked(name); ok && toolExposureOf(definition) == extension.ToolExposureHidden {
			continue
		}
		plan.tools = append(plan.tools, tool)
	}
	for _, tool := range plan.tools {
		for _, entry := range s.toolRegistry.entries {
			if entry.tool.Name() == tool.Name() && entry.registration.Definition.PrepareLoadout != nil {
				path := ""
				if source, ok := entry.registration.SourceInfo.(icodingagent.PiSourceInfo); ok {
					path = source.Path
				}
				plan.hooks = append(plan.hooks, loadoutHook{entry.registration.Definition.PrepareLoadout, path})
			}
		}
	}
	if len(plan.hooks) == 0 {
		return plan
	}
	active := make(map[string]struct{}, len(plan.tools))
	declared := make([]extension.AgentTool, 0, len(plan.tools))
	for _, tool := range plan.tools {
		active[tool.Name()] = struct{}{}
		if definition, ok := s.toolDefinitionLocked(tool.Name()); ok {
			declared = append(declared, toolView(definition))
		}
	}
	var callable, registered []extension.AgentTool
	for _, entry := range s.callableEntriesLocked(active) {
		callable = append(callable, toolView(entry.registration.Definition))
	}
	exposures := map[string]extension.ToolExposure{}
	namespaces := map[string]*extension.ToolNamespace{}
	for _, entry := range s.toolRegistry.entries {
		definition := entry.registration.Definition
		registered = append(registered, toolView(definition))
		if _, known := exposures[definition.Name]; !known {
			exposures[definition.Name] = toolExposureOf(definition)
			namespaces[definition.Name] = definition.Namespace
		}
	}
	plan.loadout = extension.ToolLoadout{
		Declared: declared, Callable: callable, Registered: registered,
		GetExposure: func(name string) extension.ToolExposure {
			if exposure, ok := exposures[name]; ok {
				return exposure
			}
			return extension.ToolExposureDirect
		},
		GetNamespace: func(name string) *extension.ToolNamespace { return namespaces[name] },
	}
	return plan
}

// runLoadoutHooks runs the hooks of a plan in order and merges their changes: later descriptions win, hidden declarations add up. A failing hook is reported and changes nothing. The caller does not hold toolRegistryMu.
func (s *Session) runLoadoutHooks(plan toolLoadoutPlan) (map[string]string, map[string]struct{}) {
	descriptions := map[string]string{}
	hidden := map[string]struct{}{}
	for _, h := range plan.hooks {
		changes, err := runPrepareLoadout(h.prepare, plan.loadout)
		if err != nil {
			if runner := s.currentRunner(); runner != nil {
				runner.EmitError(&extension.ExtensionError{ExtensionPath: h.sourcePath, Event: "prepare_loadout", Error: err.Error(), Stack: err.Error() + "\n" + string(debug.Stack())})
			}
			continue
		}
		if changes == nil {
			continue
		}
		maps.Copy(descriptions, changes.Descriptions)
		for _, name := range changes.HiddenDeclarations {
			hidden[name] = struct{}{}
		}
	}
	return descriptions, hidden
}

// runPrepareLoadout calls a hook and reports a panic as its error, as upstream's try/catch does.
func runPrepareLoadout(hook extension.ToolPrepareLoadoutFunc, loadout extension.ToolLoadout) (changes *extension.ToolLoadoutChanges, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return hook(loadout), nil
}

// hideDeclarations removes the declarations that `prepareLoadout` hooks hide from every request. The whole transcript is filtered with the current set, so the projected declarations stay consistent across requests and only change when the loadout does.
//
// upstream: agent-session.ts:1683-1706 (_installHiddenDeclarationsProjection)
func (s *Session) hideDeclarations(messages []ai.Message) []ai.Message {
	hiddenSet := s.loadout.hidden.Load()
	if hiddenSet == nil || len(*hiddenSet) == 0 {
		return messages
	}
	hidden := *hiddenSet
	keptSchemas := func(tools []ai.ToolSchema) []ai.ToolSchema {
		var kept []ai.ToolSchema
		for _, tool := range tools {
			if _, isHidden := hidden[tool.Name]; !isHidden {
				kept = append(kept, tool)
			}
		}
		return kept
	}
	keptReferences := func(tools []ai.ToolReference) []ai.ToolReference {
		var kept []ai.ToolReference
		for _, tool := range tools {
			if _, isHidden := hidden[tool.Name]; !isHidden {
				kept = append(kept, tool)
			}
		}
		return kept
	}
	out := make([]ai.Message, len(messages))
	for i, message := range messages {
		system, ok := message.(ai.SystemMessage)
		if !ok || len(system.ToolsAdded) == 0 && len(system.ToolsRemoved) == 0 {
			out[i] = message
			continue
		}
		system.ToolsAdded, system.ToolsRemoved = keptSchemas(system.ToolsAdded), keptReferences(system.ToolsRemoved)
		out[i] = system
	}
	return out
}

// callableTools is the callable tools, bound to this Session, for nested calls.
func (s *Session) callableTools() []agent.AgentTool {
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	var tools []agent.AgentTool
	for _, entry := range s.callableEntriesLocked(nil) {
		tools = append(tools, s.bindTool(entry.tool))
	}
	return tools
}

// getCallableTools backs `ctx.tools`.
//
// upstream: agent-session.ts:3382 (getCallableTools: () => this._getCallableTools())
func (s *Session) getCallableTools() []extension.AgentTool {
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	views := []extension.AgentTool{}
	for _, entry := range s.callableEntriesLocked(nil) {
		views = append(views, toolView(entry.registration.Definition))
	}
	return views
}

// sessionNestedHost runs the nested calls of a Session.
type sessionNestedHost struct{ session *Session }

func (h sessionNestedHost) GetTools() []agent.AgentTool { return h.session.callableTools() }
func (h sessionNestedHost) IsSequential() bool {
	return h.session.agent.ToolExecutionMode() == agent.ToolModeSequential
}

// RunToolCall runs the call through the agent's tool pipeline with the session's hooks, against the callable tools.
//
// upstream: agent-session.ts:_executeNestedToolCall (runToolCall with beforeToolCall/afterToolCall bound to parentId)
func (h sessionNestedHost) RunToolCall(ctx context.Context, toolCall agent.AgentToolCall, parentToolCallID string, onUpdate agent.ToolUpdateSink) (agent.AgentToolCallOutcome, error) {
	s := h.session
	if lastAssistantMessage(s.agent.Messages()) == nil {
		return agent.AgentToolCallOutcome{ToolCall: toolCall, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "No assistant message issued this call"}}, Details: map[string]any{}}, IsError: true}, nil
	}
	hooks := agent.ToolCallHooks{
		BeforeToolCall: append(append([]agent.BeforeToolCallHook(nil), s.callerHooks.beforeToolCall...), func(ctx context.Context, id, name string, args json.RawMessage) agent.ToolCallHookResult {
			return s.toolCallHook(ctx, id, parentToolCallID, name, args)
		}),
		AfterToolCall: append(append([]agent.AfterToolCallHook(nil), s.callerHooks.afterToolCall...), func(ctx context.Context, id, name string, args json.RawMessage, result agent.AgentToolResult) agent.AfterToolCallResult {
			return s.toolResultHook(ctx, id, parentToolCallID, name, args, result)
		}),
		PrepareToolResult: s.prepareToolResult,
	}
	return agent.RunToolCall(ctx, toolCall, agent.RunToolCallOptions{ToolCallHooks: hooks, Tools: h.session.callableTools(), OnUpdate: onUpdate})
}

// Emit sends a nested call's event to the extensions and to the session's listeners, as upstream's `emit` does (`extensionRunner.emit(event)` then `_emit(event)`).
func (h sessionNestedHost) Emit(event agent.AgentEvent) {
	s := h.session
	s.loadout.emitMu.Lock()
	defer s.loadout.emitMu.Unlock()
	_ = s.handleAgentEvent(context.Background(), event)
}

func (s *Session) nestedToolCalls() *NestedToolCallRunner {
	s.loadout.nestedOnce.Do(func() { s.loadout.nested.Store(NewNestedToolCallRunner(sessionNestedHost{s})) })
	return s.loadout.nested.Load()
}

// executeNestedToolCall runs a call that the tool call callerID made through `ctx.executeTool()`. It goes through the agent's tool pipeline with the session's hooks, against the callable tools.
//
// upstream: agent-session.ts:_executeNestedToolCall
func (s *Session) executeNestedToolCall(ctx context.Context, callerID, name string, args json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
	var onUpdate agent.ToolUpdateSink
	switch callback := options.OnUpdate.(type) {
	case agent.ToolUpdateSink:
		onUpdate = callback
	case agent.ToolUpdateCallback:
		onUpdate = func(partial agent.AgentToolResult) error { callback(partial); return nil }
	case func(agent.AgentToolResult):
		onUpdate = func(partial agent.AgentToolResult) error { callback(partial); return nil }
	}
	outcome, err := s.nestedToolCalls().Execute(ctx, callerID, name, args, NestedToolCallOptions{OnUpdate: onUpdate})
	if err != nil {
		return extension.AgentToolCallOutcome{}, err
	}
	return extension.AgentToolCallOutcome{ToolCall: outcome.ToolCall, Result: outcome.Result, IsError: outcome.IsError}, nil
}

// ToolActions is the ExecuteTool, GetCallableTools and AppendEntry of the extension context actions. A host that runs extensions in another process binds them to its bridge, so a nested call from a subprocess extension reaches this session; a declarative tool reaches AppendEntry through its tool context.
//
// upstream: agent-session.ts:3382-3383 (the executeTool and getCallableTools actions bound to the runner), 3339 (getSettings), 3321-3327 (appendEntry)
func (s *Session) ToolActions() extension.ToolActions {
	return extension.ToolActions{ExecuteTool: s.executeNestedToolCall, GetCallableTools: s.getCallableTools, AppendEntry: s.appendExtensionEntry}
}

// appendExtensionEntry is upstream's `appendEntry` action of bindCore.
//
// upstream: agent-session.ts:3321-3327
func (s *Session) appendExtensionEntry(customType string, data any) error {
	_, err := s.AppendCustomEntry(customType, data)
	return err
}

// recordNestedCalls records the calls a tool made through `ctx.executeTool()` and their usage on its result message.
//
// upstream: agent-session.ts:1062-1074 (_handleAgentEvent, `message_start` of a toolResult). Go difference forced by the agent loop (agent/agent_loop.go startEventMessage): message_start carries its own copy of the message, while message_end shares the message with the transcript and the persisted entry. The record is taken at message_start and applied to that copy for listeners, then applied to the shared message at message_end, before anything reads or persists it.
func (s *Session) recordNestedCalls(event agent.AgentEvent) {
	nested := s.loadout.nested.Load()
	if nested == nil {
		return
	}
	switch event := event.(type) {
	case agent.MessageStartEvent:
		message := event.Message.ToolResult
		if message == nil {
			return
		}
		summary := nested.TakeRecord(message.ToolCallID)
		if summary == nil {
			return
		}
		s.loadout.pendingMu.Lock()
		if s.loadout.pending == nil {
			s.loadout.pending = map[string]*NestedCallSummary{}
		}
		s.loadout.pending[message.ToolCallID] = summary
		s.loadout.pendingMu.Unlock()
		applyNestedSummary(message, summary)
	case agent.MessageEndEvent:
		message := event.Message.ToolResult
		if message == nil {
			return
		}
		s.loadout.pendingMu.Lock()
		summary := s.loadout.pending[message.ToolCallID]
		delete(s.loadout.pending, message.ToolCallID)
		s.loadout.pendingMu.Unlock()
		if summary != nil {
			applyNestedSummary(message, summary)
		}
	case agent.AgentEndEvent:
		nested.Clear()
		s.loadout.pendingMu.Lock()
		clear(s.loadout.pending)
		s.loadout.pendingMu.Unlock()
	}
}

// applyNestedSummary sets nestedCalls on a tool result message and adds the summed usage of the nested results to its usage.
func applyNestedSummary(message *agent.ToolResultMessage, summary *NestedCallSummary) {
	if summary.Calls != nil {
		message.NestedCalls = summary.Calls
	}
	if summary.Usage != nil {
		if message.Usage != nil {
			combined := combineUsage(*message.Usage, *summary.Usage)
			message.Usage = &combined
		} else {
			message.Usage = summary.Usage
		}
	}
}
