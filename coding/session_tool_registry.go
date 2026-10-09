package coding

// Ports packages/coding-agent/src/core/agent-session.ts.
// Ports packages/coding-agent/src/core/sdk.ts.
// Ports packages/coding-agent/src/core/extensions/wrapper.ts.
// Ports packages/coding-agent/src/core/tools/tool-definition-wrapper.ts.

import (
	"context"
	"encoding/json"
	"errors"
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
	// A registered extension tool wrapped with its runner (WrapRegisteredTool) creates its own.
	if wrapped, ok := tool.AgentTool.(*bridgeTool); !ok || wrapped.runner == nil {
		if runner := tool.session.currentRunner(); runner != nil {
			ctx = runner.ToolCallContext(ctx, id)
		}
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
	// filter is the compiled `--tools` and `--exclude-tools` entries, names or `*` patterns.
	filter         extension.ToolFilter
	skipExtensions bool
	// usesDefaultTools reports that the initial tools come from the defaultTools setting, so a reload activates tools newly added to it.
	// upstream: agent-session.ts:263-267 (usesDefaultTools), sdk.ts:448
	usesDefaultTools bool
	// defaultToolModifiers are applied to the defaultTools setting wherever the registry reads it (agent-session.ts:3667-3670).
	defaultToolModifiers []string
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
	return icodingagent.CreateSyntheticSourceInfo(path, icodingagent.SyntheticSourceInfoOptions{Source: source})
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
		return tool.Execute(ctx, id, args, onUpdate)
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

func createSessionToolRegistry(services *AgentSessionServices, opts SessionOptions) (sessionToolRegistry, []agent.AgentTool, error) {
	registry := sessionToolRegistry{allowed: maps.Clone(opts.AllowedTools), excluded: maps.Clone(opts.ExcludedTools), skipExtensions: opts.skipExtensionTools,
		usesDefaultTools: usesDefaultTools(opts), defaultToolModifiers: slices.Clone(opts.DefaultToolModifiers)}
	if opts.toolRegistry != nil {
		registry = *opts.toolRegistry
		registry.base = slices.Clone(registry.base)
		registry.custom = slices.Clone(registry.custom)
		registry.entries = slices.Clone(registry.entries)
		registry.allowed = maps.Clone(registry.allowed)
		registry.excluded = maps.Clone(registry.excluded)
		registry.filter = extension.NewToolFilter(registry.allowed, registry.excluded)
		return registry, registry.tools(), nil
	}
	// upstream: sdk.ts:280-281; the tools option that changes the default selection is DefaultToolModifiers here.
	if problem := icodingagent.GetToolListError(opts.DefaultToolModifiers); problem != "" {
		return registry, nil, fmt.Errorf("Invalid tools option: %s", problem)
	}
	// Pi's one tools list holds names and +name/-name entries together; here the names are AllowedTools, so both at once is the mixed list getToolListError rejects (settings-manager.ts:230).
	if opts.AllowedTools != nil && opts.NoTools == "" && len(opts.DefaultToolModifiers) > 0 {
		return registry, nil, errors.New("Invalid tools option: tool names cannot be mixed with +name or -name entries")
	}
	if opts.NoTools != "" && opts.NoTools != "all" && opts.NoTools != "builtin" {
		return registry, nil, fmt.Errorf("invalid noTools selection %q", opts.NoTools)
	}
	// A `tools` list of only `+name` and `-name` entries changes the default selection instead of replacing it (none under NoTools); a list that mixes plain names or patterns in is rejected.
	// upstream: sdk.ts:279-295 (getToolListError, toolModifiers, selectedToolNames, allowedToolNames)
	if problem := icodingagent.GetToolListError(opts.DefaultToolModifiers); problem != "" {
		return registry, nil, fmt.Errorf("Invalid tools option: %s", problem)
	}
	var modifiedSelection []string
	if slices.ContainsFunc(opts.DefaultToolModifiers, icodingagent.IsToolModifier) {
		var defaultToolNames []string
		if opts.NoTools == "" {
			defaultToolNames = services.SettingsManager().ResolvedDefaultTools()
		}
		modifiedSelection = icodingagent.ApplyToolModifiers(defaultToolNames, opts.DefaultToolModifiers)
	}
	if opts.NoTools == "all" && opts.AllowedTools == nil {
		registry.allowed = map[string]struct{}{}
		for _, name := range modifiedSelection {
			registry.allowed[name] = struct{}{}
		}
	}
	registry.filter = extension.NewToolFilter(registry.allowed, registry.excluded)
	var activeNames []string
	if !opts.SkipBuiltinTools {
		baseTools := tools.CreateAllTools(services.CWD(), tools.ToolsOptionsFromSettings(services.Settings(), filepath.Join(services.AgentDir(), "bin")))
		var overrideKeys []string
		if opts.BaseToolsOverride != nil {
			baseTools = baseTools[:0:0]
			for _, entry := range opts.BaseToolsOverride {
				overrideKeys = append(overrideKeys, entry.Name)
				baseTools = append(baseTools, entry.Tool)
			}
		}
		for _, tool := range baseTools {
			definition := builtinToolDefinition(tool)
			registry.base = append(registry.base, sessionToolEntry{tool: tool, registration: extension.RegisteredTool{Definition: definition, SourceInfo: syntheticToolSource(tool.Name(), "builtin")}})
		}
		switch {
		case modifiedSelection != nil && opts.AllowedTools == nil && opts.InitialActiveToolNames == nil:
			activeNames = slices.Clone(modifiedSelection)
		case opts.AllowedTools != nil:
			// upstream: agent-session.ts:3552-3562: the initial names keep the caller's order; the registered tools the allowlist names or matches follow in registry order.
			activeNames = append(activeNames, opts.InitialActiveToolNames...)
			for _, entry := range registry.base {
				// Naming or matching a built-in tool activates it (agent-session.ts _refreshToolRegistry).
				if registry.filter.Names(entry.tool.Name()) {
					activeNames = append(activeNames, entry.tool.Name())
				}
			}
		case opts.InitialActiveToolNames != nil:
			// upstream: sdk.ts:274-276, agent-session.ts:3552-3554: the initial names, which may also name extension tools, stay in the caller's order; selectTools keeps the names the registry admits. An explicit list outranks NoTools (initialActiveToolNames = tools ?? (noTools ? [] : defaults)).
			activeNames = slices.Clone(opts.InitialActiveToolNames)
		case opts.NoTools != "":
		case opts.BaseToolsOverride != nil:
			// upstream: agent-session.ts:3650-3652
			activeNames = append(activeNames, overrideKeys...)
		default:
			activeNames = icodingagent.ApplyToolModifiers(services.SettingsManager().ResolvedDefaultTools(), opts.DefaultToolModifiers)
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
			tool, err := WrapRegisteredTool(registration, runner)
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
		if !r.filter.Allows(name) {
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

// BindExtensions stores the mode bindings the argument defines, always applies the stored bindings to the runner, awaits the configured session_start event and admits tools registered by its handlers before returning (upstream bindExtensions, agent-session.ts:3258-3282). Attach an event consumer before binding when handlers can send messages.
func (s *Session) BindExtensions(ctx context.Context, bindings ExtensionBindings) error {
	runner := s.currentRunner()
	if runner == nil {
		return nil
	}
	s.bindSessionExtensions(runner, s.mergeExtensionBindings(bindings))
	event := s.sessionStartEvent
	if event.Type == "" {
		event = extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	}
	if _, err := runner.Emit(ctx, event); err != nil {
		return err
	}
	// upstream: agent-session.ts:3207 (bindExtensions)
	runner.ReportUnhandledMcpServers()
	if err := s.extendResourcesFromExtensions(ctx, event.Reason); err != nil {
		return err
	}
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
	if includeAllExtensionTools && !s.toolRegistry.filter.HasAllowlist() {
		reactivated = s.toolRegistry.extensionToolNames(s.currentRunner())
	}
	for _, tool := range s.tools {
		_, known := previous[tool.Name()]
		_, reactivate := reactivated[tool.Name()]
		definition, _ := s.toolDefinitionLocked(tool.Name())
		// upstream: agent-session.ts:3553-3572: with an allowlist, naming or matching a tool activates it when it is declarable (MCP tools the allowlist keeps without matching them stay inactive); without one, a new tool activates only when it activates on registration.
		if s.toolRegistry.filter.HasAllowlist() {
			if s.toolRegistry.filter.Names(tool.Name()) && toolIsDeclarable(definition) {
				active = append(active, tool.Name())
			}
		} else if (!known || reactivate) && toolActivatesOnRegistration(definition) {
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

// ReloadOption configures Session.Reload (agent-session.ts reload(options)).
type ReloadOption func(*reloadOptions)

type reloadOptions struct {
	beforeSessionStart func(context.Context) error
}

// WithBeforeSessionStart runs fn after the new runner is bound and before it receives session_start, only when the mode bound its UI or command
// actions (reload's options.beforeSessionStart, agent-session.ts:3676-3678). An error from fn fails the reload.
func WithBeforeSessionStart(fn func(context.Context) error) ReloadOption {
	return func(o *reloadOptions) { o.beforeSessionStart = fn }
}

// Reload is AgentSession.reload (agent-session.ts:3640-3685): the old extensions receive session_shutdown with reason reload and go stale;
// the settings and the resource loader are read again; the loader's extensions build a new runner that keeps the previous flag values; the
// tools a settings reload newly added to defaultTools activate; and, when the mode bound its UI or command actions, the new runner gets them
// and session_start with reason reload, then the resources its resources_discover handlers contribute. A failure before the new runner exists
// leaves the old one stale, as Pi does.
func (s *Session) Reload(ctx context.Context, options ...ReloadOption) error {
	var reload reloadOptions
	for _, option := range options {
		option(&reload)
	}
	previous := s.currentRunner()
	var flagValues map[string]any
	if previous != nil {
		flagValues = previous.GetFlagValues()
		if previous.HasHandlers(icodingagent.EventSessionShutdown) {
			if _, err := previous.Emit(ctx, extension.SessionShutdownEvent{Type: icodingagent.EventSessionShutdown, Reason: "reload"}); err != nil {
				return err
			}
		}
		previous.Invalidate("")
	}
	s.ReloadSettings()
	// The tools the settings reload staged activate in the runtime rebuild below; any other exit drops them (agent-session.ts:3598-3609).
	defer s.DiscardAddedDefaultTools()
	// The API provider registry returns to the built-in providers before the extensions load again, so a provider a removed extension registered is gone (agent-session.ts:3656 resetApiProviders).
	ai.ResetAPIProviders()
	loader := s.ResourceLoader()
	if err := loader.Reload(); err != nil {
		return err
	}
	loaded := loader.GetExtensions()
	runner, err := s.ReloadExtensions(loaded.Extensions, loaded.Runtime)
	if err != nil {
		return err
	}
	for name, value := range flagValues {
		runner.SetFlagValue(name, value)
	}
	bindings := s.extensionBindings.Load()
	if bindings == nil {
		return nil
	}
	s.bindSessionExtensions(runner, *bindings)
	// upstream: agent-session.ts:3688-3698: only a session whose mode bound a UI context, command actions, a shutdown handler or an error listener replays session_start on reload.
	if !bindings.hasBindings() {
		return nil
	}
	if reload.beforeSessionStart != nil {
		if err := reload.beforeSessionStart(ctx); err != nil {
			return err
		}
	}
	if _, err := runner.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: "reload"}); err != nil {
		return err
	}
	runner.ReportUnhandledMcpServers()
	return s.extendResourcesFromExtensions(ctx, "reload")
}

// usesDefaultTools reports whether the session's initial tools come from the defaultTools setting.
// upstream: sdk.ts:472 `(options.tools === undefined || toolModifiers !== undefined) && !options.noTools`; agent-session.ts:500 `config.usesDefaultTools ?? false`, so a session built on a base tool override does not use them unless it says so.
func usesDefaultTools(opts SessionOptions) bool {
	if opts.UsesDefaultTools != nil {
		return *opts.UsesDefaultTools
	}
	return opts.BaseToolsOverride == nil && opts.AllowedTools == nil && opts.NoTools == "" && !opts.SkipBuiltinTools
}
