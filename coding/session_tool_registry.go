package coding

// Ports packages/coding-agent/src/core/agent-session.ts.
// Ports packages/coding-agent/src/core/sdk.ts.
// Ports packages/coding-agent/src/core/extensions/wrapper.ts.
// Ports packages/coding-agent/src/core/tools/tool-definition-wrapper.ts.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

type sessionBoundTool struct {
	agent.AgentTool
	session *Session
	// description replaces the tool's declared description when set, as a prepareLoadout hook does.
	description *string
}

// Schema is the wrapped tool's schema with the description a prepareLoadout hook set.
//
// upstream: agent-session.ts:1551-1557 (declared = { ...tool, description })
func (tool *sessionBoundTool) Schema() ai.ToolSchema {
	schema := tool.AgentTool.Schema()
	if tool.description != nil {
		schema.Description = *tool.description
	}
	return schema
}

type sessionArgumentSchema interface {
	ArgumentSchema() json.RawMessage
}

type sessionBoundArgumentTool struct {
	sessionBoundTool
	schema sessionArgumentSchema
}

func (tool *sessionBoundArgumentTool) ArgumentSchema() json.RawMessage {
	return tool.schema.ArgumentSchema()
}

// bindTool preserves optional validation metadata without manufacturing the capability on ordinary tools.
func (s *Session) bindTool(tool agent.AgentTool, description ...string) agent.AgentTool {
	bound := sessionBoundTool{AgentTool: tool, session: s}
	if len(description) > 0 {
		bound.description = &description[0]
	}
	if schema, ok := tool.(sessionArgumentSchema); ok {
		return &sessionBoundArgumentTool{sessionBoundTool: bound, schema: schema}
	}
	return &bound
}

func (tool *sessionBoundTool) Execute(ctx context.Context, id string, args json.RawMessage, update agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	if _, bound := agent.ToolEnvironmentFrom(ctx); !bound {
		session := tool.session
		env := agent.ToolEnvironment{SessionID: session.ID(), SessionFile: session.Path(), ThinkingLevel: string(session.ThinkingLevel())}
		if model := session.Model(); model != nil {
			env.Model = model.ID
			env.Provider = providerID(model)
			env.InputLimits = model.InputLimits.Clone()
			supports := model.Capabilities.SupportsImages
			if model.Input != nil {
				supports = slices.Contains(model.Input, "image")
			}
			env.SupportsImages = &supports
		}
		ctx = agent.WithToolEnvironment(ctx, env)
	}
	// Every call gets its own tool context, including a nested call an extension made, as upstream's wrapToolDefinition creates one per call.
	//
	// upstream: wrapper.ts:14-19, tool-definition-wrapper.ts:7-30
	if runner := tool.session.currentRunner(); runner != nil {
		ctx = runner.CreateToolContext(ctx, id)
	}
	return tool.AgentTool.Execute(ctx, id, args, update)
}

func (tool *sessionBoundTool) PrepareArguments(args json.RawMessage) (json.RawMessage, error) {
	if prepare, ok := tool.AgentTool.(agent.ArgumentPreparer); ok {
		return prepare.PrepareArguments(args)
	}
	return args, nil
}

func (tool *sessionBoundTool) ReserveMutationOrder(args json.RawMessage) (*agent.MutationTicket, bool) {
	if ordered, ok := tool.AgentTool.(agent.QueueOrderable); ok {
		return ordered.ReserveMutationOrder(args)
	}
	return nil, false
}

type sessionToolEntry struct {
	tool         agent.AgentTool
	registration extension.RegisteredTool
}

type sessionToolRegistry struct {
	base, custom      []sessionToolEntry
	entries           []sessionToolEntry
	allowed, excluded map[string]struct{}
	skipExtensions    bool
	// usesDefaultTools reports that the initial tools come from the defaultTools setting, so a reload activates tools newly added to it.
	// upstream: agent-session.ts:263-267 (usesDefaultTools), sdk.ts:448
	usesDefaultTools bool
	// addedDefaultTools are the tools a settings reload newly added to defaultTools; the next RefreshTools activates them.
	addedDefaultTools []string
}

// syntheticToolSource is createSyntheticSourceInfo for a tool of a session: `builtin:<name>` for a built-in tool and `<sdk:name>` for an SDK tool.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/agent-session.ts:3425-3440,3470-3474.
func syntheticToolSource(name, source string) icodingagent.PiSourceInfo {
	path := "<" + source + ":" + name + ">"
	if source == "builtin" {
		path = icodingagent.BuiltinPathPrefix + name
	}
	return icodingagent.PiSourceInfo{Path: path, Source: source, Scope: "temporary", Origin: "top-level"}
}

func toolDefinition(tool agent.AgentTool) (extension.ToolDefinition, error) {
	if bridged, ok := tool.(*bridgeTool); ok {
		return bridged.def, nil
	}
	schema := tool.Schema()
	parameters, err := json.Marshal(schema.Parameters)
	if err != nil {
		return extension.ToolDefinition{}, err
	}
	var sampling json.RawMessage
	if schema.ConstrainedSampling != nil {
		sampling, err = json.Marshal(schema.ConstrainedSampling)
		if err != nil {
			return extension.ToolDefinition{}, err
		}
	}
	label := tool.Label()
	if label == "" {
		label = tool.Name()
	}
	definition := extension.ToolDefinition{Name: tool.Name(), Label: label, Description: schema.Description, Parameters: parameters, ConstrainedSampling: sampling, PromptSnippet: prompts.DefaultToolSnippets()[tool.Name()], PromptGuidelines: schema.PromptGuidelines, ExecutionMode: extension.ToolExecutionMode(tool.ExecutionMode()), Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		update, _ := onUpdate.(agent.ToolUpdateCallback)
		return tool.Execute(ctx, id, args, update)
	}}
	if preparer, ok := tool.(agent.ArgumentPreparer); ok {
		definition.PrepareArguments = preparer.PrepareArguments
	}
	// upstream: tool-definition-wrapper.ts:52 (createToolDefinitionFromAgentTool copies outputSchema)
	// upstream: bash.ts:262 (outputSchema: bashOutputSchema); codemode resolves a nested call of such a tool to its structuredContent, also for a returned isError (codemode/execute.ts:201-209).
	if provider, ok := tool.(agent.OutputSchemaProvider); ok {
		definition.OutputSchema = provider.OutputSchema()
	}
	return definition, nil
}

func createSessionToolRegistry(services *Services, opts SessionOptions) (sessionToolRegistry, []agent.AgentTool, error) {
	registry := sessionToolRegistry{allowed: maps.Clone(opts.AllowedTools), excluded: maps.Clone(opts.ExcludedTools), skipExtensions: opts.skipExtensionTools,
		usesDefaultTools: opts.AllowedTools == nil && opts.NoTools == "" && !opts.SkipBuiltinTools}
	if opts.toolRegistry != nil {
		registry = *opts.toolRegistry
		registry.base = slices.Clone(registry.base)
		registry.custom = slices.Clone(registry.custom)
		registry.entries = slices.Clone(registry.entries)
		registry.allowed = maps.Clone(registry.allowed)
		registry.excluded = maps.Clone(registry.excluded)
		return registry, registry.tools(), nil
	}
	if opts.NoTools != "" && opts.NoTools != "all" && opts.NoTools != "builtin" {
		return registry, nil, fmt.Errorf("invalid noTools selection %q", opts.NoTools)
	}
	if opts.NoTools == "all" && opts.AllowedTools == nil {
		registry.allowed = map[string]struct{}{}
	}
	var activeNames []string
	if !opts.SkipBuiltinTools {
		for _, tool := range tools.CreateAllTools(services.CWD(), services.Settings(), filepath.Join(services.AgentDir(), "bin")) {
			definition, err := toolDefinition(tool)
			if err != nil {
				return registry, nil, err
			}
			// Builtin definitions omit executionMode; the agent supplies the parallel default.
			definition.ExecutionMode = ""
			registry.base = append(registry.base, sessionToolEntry{tool: tool, registration: extension.RegisteredTool{Definition: definition, SourceInfo: syntheticToolSource(tool.Name(), "builtin")}})
		}
		switch {
		case opts.AllowedTools != nil:
			for _, entry := range registry.base {
				if _, ok := opts.AllowedTools[entry.tool.Name()]; ok {
					activeNames = append(activeNames, entry.tool.Name())
				}
			}
		case opts.NoTools != "":
		case opts.ActiveBuiltinTools != nil:
			// upstream: sdk.ts:264-269, agent-session.ts:3487-3506, 1508-1511: the initial selection is the defaultTools list in its order, which also names extension tools; selectTools keeps the names the registry admits. The CLI's set is that list, so its names keep the resolved defaultTools order; the rest follow in registry order, then by name.
			pending := maps.Clone(opts.ActiveBuiltinTools)
			for _, name := range services.SettingsManager().ResolvedDefaultTools() {
				if _, ok := pending[name]; ok {
					activeNames = append(activeNames, name)
					delete(pending, name)
				}
			}
			for _, entry := range registry.base {
				if _, ok := pending[entry.tool.Name()]; ok {
					activeNames = append(activeNames, entry.tool.Name())
					delete(pending, entry.tool.Name())
				}
			}
			activeNames = append(activeNames, slices.Sorted(maps.Keys(pending))...)
		default:
			activeNames = services.SettingsManager().ResolvedDefaultTools()
		}
	}
	for _, tool := range opts.Tools {
		definition, err := toolDefinition(tool)
		if err != nil {
			return registry, nil, err
		}
		if _, defined := tool.(*bridgeTool); !defined {
			definition.PromptSnippet = ""
			definition.PromptGuidelines = nil
		}
		registry.custom = append(registry.custom, sessionToolEntry{tool: tool, registration: extension.RegisteredTool{Definition: definition, SourceInfo: syntheticToolSource(tool.Name(), "sdk")}})
		activeNames = append(activeNames, tool.Name())
	}
	for _, definition := range opts.CustomTools {
		registration := extension.RegisteredTool{Definition: definition, SourceInfo: syntheticToolSource(definition.Name, "sdk")}
		tool, err := newBridgeTool(registration)
		if err != nil {
			return registry, nil, err
		}
		registry.custom = append(registry.custom, sessionToolEntry{tool: tool, registration: registration})
		activeNames = append(activeNames, definition.Name)
	}
	if err := registry.refresh(opts.Runner); err != nil {
		return registry, nil, err
	}
	if opts.Runner != nil && !registry.skipExtensions {
		for _, tool := range opts.Runner.Tools() {
			if ExtensionToolStartsActive(tool.Definition, registry.allowed) {
				activeNames = append(activeNames, tool.Definition.Name)
			}
		}
	}
	return registry, registry.selectTools(activeNames), nil
}

func (r *sessionToolRegistry) refresh(runner *inproc.Runner) error {
	entries := slices.Clone(r.base)
	if runner != nil && !r.skipExtensions {
		for _, registration := range runner.Tools() {
			source, _ := runner.ToolSourceInfo(registration.Definition.Name)
			registration.SourceInfo = source
			tool, err := newBridgeTool(registration)
			if err != nil {
				return err
			}
			entries = append(entries, sessionToolEntry{tool: tool, registration: registration})
		}
	}
	entries = append(entries, r.custom...)
	indexes := make(map[string]int)
	var admitted []sessionToolEntry
	for _, entry := range entries {
		name := entry.tool.Name()
		if _, blocked := r.excluded[name]; blocked {
			continue
		}
		if _, allowed := r.allowed[name]; r.allowed != nil && !allowed {
			continue
		}
		if index, exists := indexes[name]; exists {
			admitted[index] = entry
		} else {
			indexes[name] = len(admitted)
			admitted = append(admitted, entry)
		}
	}
	r.entries = admitted
	return nil
}

// extensionToolNames names the tools of runner and the SDK custom tools, the registrations upstream's _refreshToolRegistry wraps as extension tools (agent-session.ts:3434-3441).
func (r *sessionToolRegistry) extensionToolNames(runner *inproc.Runner) map[string]struct{} {
	names := make(map[string]struct{})
	if runner != nil && !r.skipExtensions {
		for _, registration := range runner.Tools() {
			names[registration.Definition.Name] = struct{}{}
		}
	}
	for _, entry := range r.custom {
		names[entry.tool.Name()] = struct{}{}
	}
	return names
}

func (r *sessionToolRegistry) selectTools(names []string) []agent.AgentTool {
	result := make([]agent.AgentTool, 0, len(names))
	seen := make(map[string]struct{})
	registry := make(map[string]agent.AgentTool, len(r.entries))
	for _, entry := range r.entries {
		registry[entry.tool.Name()] = entry.tool
	}
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		if tool := registry[name]; tool != nil {
			result = append(result, tool)
			seen[name] = struct{}{}
		}
	}
	return result
}
func (r *sessionToolRegistry) tools() []agent.AgentTool {
	result := make([]agent.AgentTool, len(r.entries))
	for i, entry := range r.entries {
		result[i] = entry.tool
	}
	return result
}

// GetAllTools returns every admitted definition, including inactive builtins, with its registration metadata.
func (s *Session) GetAllTools() []extension.ToolInfo {
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	result := make([]extension.ToolInfo, 0, len(s.toolRegistry.entries))
	for _, entry := range s.toolRegistry.entries {
		definition := entry.registration.Definition
		info := extension.ToolInfo{Name: definition.Name, Description: definition.Description, Parameters: slices.Clone(definition.Parameters), PromptGuidelines: slices.Clone(definition.PromptGuidelines), SourceInfo: entry.registration.SourceInfo, Exposure: toolExposureOf(definition)}
		if definition.Namespace != nil {
			info.Namespace = new(*definition.Namespace)
		}
		if definition.Annotations != nil {
			info.Annotations = new(*definition.Annotations)
		}
		result = append(result, info)
	}
	return result
}

// GetToolDefinition returns the admitted definition for name.
func (s *Session) GetToolDefinition(name string) (extension.ToolDefinition, bool) {
	s.toolRegistryMu.RLock()
	defer s.toolRegistryMu.RUnlock()
	for _, entry := range s.toolRegistry.entries {
		if entry.tool.Name() == name {
			return entry.registration.Definition, true
		}
	}
	return extension.ToolDefinition{}, false
}

// BindExtensions applies optional mode bindings, awaits the configured session_start event and admits tools registered by its handlers before returning. Attach an event consumer before binding when handlers can send messages.
func (s *Session) BindExtensions(ctx context.Context, bindings ...ExtensionBindings) error {
	runner := s.currentRunner()
	if runner == nil {
		return nil
	}
	if len(bindings) > 0 {
		s.bindSessionExtensions(runner, bindings[0])
	}
	event := s.sessionStartEvent
	if event.Type == "" {
		event = extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	}
	if _, err := runner.Emit(ctx, event); err != nil {
		return err
	}
	// upstream: agent-session.ts:3207 (bindExtensions)
	runner.ReportUnhandledMcpServers()
	return s.RefreshTools()
}

// RefreshTools rebuilds registered definitions and their prompt contributions, preserves active selection, and activates newly admitted names.
func (s *Session) RefreshTools() error {
	return s.refreshTools(false)
}

// RefreshToolsAfterReload is the tool registry rebuild of upstream's reload(): RefreshTools, except that without a tool allowlist every extension or SDK tool that activates on registration is active again, including one disabled during the session.
//
// upstream: agent-session.ts:3604-3609 (_buildRuntime with includeAllExtensionTools), 3507-3510
//
// Active tools that the reloaded extensions register later, such as MCP tools, are pending until then (upstream: agent-session.ts reload).
func (s *Session) RefreshToolsAfterReload() error {
	return s.refreshTools(true)
}

// refreshTools rebuilds the registry. Only a reload passes includeAllExtensionTools.
func (s *Session) refreshTools(includeAllExtensionTools bool) error {
	s.loadout.applyMu.Lock()
	defer s.loadout.applyMu.Unlock()
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	if includeAllExtensionTools {
		// The active tools become pending under the same locks as the rebuild, as reload() marks them and calls _buildRuntime without yielding, so no loadout change or registry refresh runs in between.
		s.addPendingTools(s.ActiveToolNames())
	}
	// upstream: agent-session.ts:3457-3459: only tools that were already activated on registration count as known, so a tool whose exposure changes to `direct` or `model-only` is activated like a new tool.
	previous := make(map[string]struct{}, len(s.tools))
	for _, tool := range s.tools {
		if definition, ok := s.toolDefinitionLocked(tool.Name()); ok && toolActivatesOnRegistration(definition) || !ok {
			previous[tool.Name()] = struct{}{}
		}
	}
	// upstream: agent-session.ts:3598-3609: a reload activates the tools it newly added to defaultTools.
	active := append(s.ActiveToolNames(), s.toolRegistry.addedDefaultTools...)
	s.toolRegistry.addedDefaultTools = nil
	if err := s.toolRegistry.refresh(s.currentRunner()); err != nil {
		return err
	}
	s.tools = s.toolRegistry.tools()
	// upstream: agent-session.ts:3507-3510: a reload without an allowlist activates every extension or SDK tool that activates on registration.
	var reactivated map[string]struct{}
	if includeAllExtensionTools && s.toolRegistry.allowed == nil {
		reactivated = s.toolRegistry.extensionToolNames(s.currentRunner())
	}
	for _, tool := range s.tools {
		_, known := previous[tool.Name()]
		_, allowed := s.toolRegistry.allowed[tool.Name()]
		_, reactivate := reactivated[tool.Name()]
		definition, _ := s.toolDefinitionLocked(tool.Name())
		// upstream: agent-session.ts:3487-3506: naming a tool activates it when it is declarable; a new tool activates only when it activates on registration.
		if allowed && toolIsDeclarable(definition) || (!known || reactivate) && toolActivatesOnRegistration(definition) {
			active = append(active, tool.Name())
		}
	}
	// upstream: agent-session.ts _buildRuntime: pending tools that are registered now become active.
	active = append(active, s.pendingToolNames...)
	var names []string
	for _, tool := range s.toolRegistry.selectTools(active) {
		names = append(names, tool.Name())
	}
	s.setActiveToolsByName(names)
	return nil
}
