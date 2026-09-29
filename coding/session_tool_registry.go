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
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

type sessionBoundTool struct {
	agent.AgentTool
	session *Session
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
func (s *Session) bindTool(tool agent.AgentTool) agent.AgentTool {
	bound := sessionBoundTool{AgentTool: tool, session: s}
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
	if extension.FromContext(ctx) == nil {
		if runner := tool.session.currentRunner(); runner != nil {
			ctx = runner.DispatchContext(ctx)
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
	skipExtensions    bool
}

func syntheticToolSource(name, source string) icodingagent.PiSourceInfo {
	return icodingagent.PiSourceInfo{Path: "<" + source + ":" + name + ">", Source: source, Scope: "temporary", Origin: "top-level"}
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
	return definition, nil
}

func createSessionToolRegistry(services *Services, opts SessionOptions) (sessionToolRegistry, []agent.AgentTool, error) {
	registry := sessionToolRegistry{allowed: maps.Clone(opts.AllowedTools), excluded: maps.Clone(opts.ExcludedTools), skipExtensions: opts.skipExtensionTools}
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
			for _, entry := range registry.base {
				if _, ok := opts.ActiveBuiltinTools[entry.tool.Name()]; ok {
					activeNames = append(activeNames, entry.tool.Name())
				}
			}
		default:
			activeNames = services.SettingsManager().GetDefaultTools()
			if activeNames == nil {
				activeNames = []string{"read", "bash", "edit", "write"}
			}
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
			activeNames = append(activeNames, tool.Definition.Name)
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
		result = append(result, extension.ToolInfo{Name: definition.Name, Description: definition.Description, Parameters: slices.Clone(definition.Parameters), PromptGuidelines: slices.Clone(definition.PromptGuidelines), SourceInfo: entry.registration.SourceInfo})
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
	return s.RefreshTools()
}

// RefreshTools rebuilds registered definitions and their prompt contributions, preserves active selection, and activates newly admitted names.
func (s *Session) RefreshTools() error {
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	previous := make(map[string]struct{}, len(s.tools))
	for _, tool := range s.tools {
		previous[tool.Name()] = struct{}{}
	}
	active := s.ActiveToolNames()
	if err := s.toolRegistry.refresh(s.currentRunner()); err != nil {
		return err
	}
	s.tools = s.toolRegistry.tools()
	for _, tool := range s.tools {
		_, known := previous[tool.Name()]
		_, allowed := s.toolRegistry.allowed[tool.Name()]
		if !known || allowed {
			active = append(active, tool.Name())
		}
	}
	var names []string
	for _, tool := range s.toolRegistry.selectTools(active) {
		names = append(names, tool.Name())
	}
	s.setActiveToolsByName(names)
	return nil
}
