package coding

// Ports packages/coding-agent/src/core/agent-session-services.ts createAgentSessionFromServices and the createAgentSession steps of sdk.ts
// it delegates to: the model selection (a saved model that still has auth, else the initial model), the tool selection (`tools`,
// `excludeTools`, `noTools`, the `+name`/`-name` modifiers) and the session construction. NewSession takes the resolved inputs.

import (
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// CreateAgentSessionFromServicesOptions mirrors agent-session-services.ts CreateAgentSessionFromServicesOptions: the services plus the session inputs
// that must be resolved against their cwd.
type CreateAgentSessionFromServicesOptions struct {
	Services *AgentSessionServices
	// SessionManager is the session log; nil creates a new one for the services' cwd.
	SessionManager *SessionManager
	// Model is the model to use; nil restores the session's saved model or selects the initial model.
	Model *ai.Model
	// ThinkingLevel overrides the restored and configured level before clamping to the model.
	ThinkingLevel ai.ModelThinkingLevel
	ScopedModels  []ScopedModel
	// Tools is the allowlist of tool names or `*` patterns, or a list of only `+name` and `-name` entries that changes the default selection.
	// Nil means no list; a non-nil empty list is an allowlist of nothing.
	Tools []string
	// ExcludeTools is the denylist of tool names or patterns, applied after Tools.
	ExcludeTools []string
	// NoTools is "all" (no tools) or "builtin" (no default built-in tools; extension and custom tools stay enabled).
	NoTools string
	// CustomTools are SDK tool definitions registered in addition to the built-in tools.
	CustomTools []extension.ToolDefinition
	// SessionStartEvent is the session_start metadata the extension runtime starts with.
	SessionStartEvent *extension.SessionStartEvent
}

// CreateAgentSessionResult mirrors sdk.ts CreateAgentSessionResult.
type CreateAgentSessionResult struct {
	Session *Session
	// ExtensionsResult is what the services' resource loader loaded (for UI context setup).
	ExtensionsResult LoadExtensionsResult
	// ModelFallbackMessage warns that the saved model could not be restored, or that no model is available.
	ModelFallbackMessage string
}

// CreateAgentSessionFromServices creates a Session over previously created services (agent-session-services.ts:214).
func CreateAgentSessionFromServices(options CreateAgentSessionFromServicesOptions) (CreateAgentSessionResult, error) {
	return createAgentSessionOver(options, nil)
}

// CreateAgentSessionOptions mirrors sdk.ts:48-106 CreateAgentSessionOptions: everything createAgentSession needs to build its own services and the session.
// A nil or empty member is an omitted option.
type CreateAgentSessionOptions struct {
	// CWD is the working directory for project-local discovery; empty is the session manager's cwd, else the process's.
	CWD string
	// AgentDir is the global config directory; empty is the default agent directory.
	AgentDir string
	// ModelRuntime is the canonical model and auth runtime; nil creates one over AgentDir. A given runtime stays the caller's to close.
	ModelRuntime *ModelRuntime
	// SettingsManager supplies the settings; nil loads them from AgentDir and CWD.
	SettingsManager *SettingsManager
	// ResourceLoader supplies the resources the session builds its system prompt from; nil discovers them over CWD.
	ResourceLoader ResourceLoader
	// SessionManager is the session log; nil creates a new one for CWD.
	SessionManager *SessionManager

	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
	ScopedModels  []ScopedModel
	// Tools, ExcludeTools, NoTools and CustomTools are as in [CreateAgentSessionFromServicesOptions].
	Tools             []string
	ExcludeTools      []string
	NoTools           string
	CustomTools       []extension.ToolDefinition
	SessionStartEvent *extension.SessionStartEvent
}

// CreateAgentSession is sdk.ts createAgentSession: it builds the services the options name (cwd, agent directory, settings, model runtime) and a
// Session over them. Closing the Session releases the services it built; a model runtime or settings manager the caller supplied stays theirs.
func CreateAgentSession(options CreateAgentSessionOptions) (CreateAgentSessionResult, error) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: options.CWD, AgentDir: options.AgentDir, SessionManager: options.SessionManager,
		SettingsManager: options.SettingsManager, ModelRuntime: options.ModelRuntime,
	})
	if err != nil {
		return CreateAgentSessionResult{}, err
	}
	result, err := createAgentSessionOver(CreateAgentSessionFromServicesOptions{
		Services: services, SessionManager: options.SessionManager, Model: options.Model, ThinkingLevel: options.ThinkingLevel,
		ScopedModels: options.ScopedModels, Tools: options.Tools, ExcludeTools: options.ExcludeTools, NoTools: options.NoTools,
		CustomTools: options.CustomTools, SessionStartEvent: options.SessionStartEvent,
	}, options.ResourceLoader)
	if err != nil {
		services.Close()
		return CreateAgentSessionResult{}, err
	}
	result.Session.ownedServices = services
	return result, nil
}

func createAgentSessionOver(options CreateAgentSessionFromServicesOptions, resourceLoader ResourceLoader) (CreateAgentSessionResult, error) {
	services := options.Services
	if services == nil {
		return CreateAgentSessionResult{}, ErrNoServices
	}
	services.ensureSettings()
	// upstream: sdk.ts:259-260.
	if message := icodingagent.GetToolListError(options.Tools); message != "" {
		return CreateAgentSessionResult{}, errors.New("Invalid tools option: " + message)
	}
	selection := resolveSDKToolSelection(services.SettingsManager(), options.Tools, options.ExcludeTools, options.NoTools)

	manager := options.SessionManager
	if manager == nil {
		var err error
		if manager, err = SessionManagerFor(services, SessionStartOptions{}); err != nil {
			return CreateAgentSessionResult{}, err
		}
	}
	model, fallback := resolveSDKModel(services, manager, options.Model)

	session, err := NewSession(services, SessionOptions{
		SessionManager:         manager,
		Model:                  model,
		ThinkingLevel:          options.ThinkingLevel,
		ScopedModels:           options.ScopedModels,
		CustomTools:            options.CustomTools,
		NoTools:                options.NoTools,
		AllowedTools:           selection.allowed,
		InitialActiveToolNames: selection.initialActive,
		DefaultToolModifiers:   selection.modifiers,
		ExcludedTools:          selection.excluded,
		ResourceLoader:         resourceLoader,
	})
	if err != nil {
		return CreateAgentSessionResult{}, err
	}
	if options.SessionStartEvent != nil {
		session.sessionStartEvent = *options.SessionStartEvent
	}
	return CreateAgentSessionResult{Session: session, ExtensionsResult: session.resourceLoader.Load().loader.GetExtensions(), ModelFallbackMessage: fallback}, nil
}

// sdkToolSelection is the tool state sdk.ts:259-276 derives from the tool options.
type sdkToolSelection struct {
	// allowed is allowedToolNames; nil places no restriction, a non-nil empty set blocks every tool.
	allowed map[string]struct{}
	// excluded is excludedToolNames.
	excluded map[string]struct{}
	// initialActive is initialActiveToolNames, filtered by the exclusions.
	initialActive []string
	// modifiers are the `+name`/`-name` entries a settings reload applies to the new defaultTools.
	modifiers []string
}

func resolveSDKToolSelection(settings *SettingsManager, tools, excludeTools []string, noTools string) sdkToolSelection {
	defaultToolNames := []string{}
	if noTools == "" {
		defaultToolNames = settings.ResolvedDefaultTools()
	}
	// A list of only `+name`/`-name` entries changes the default selection instead of replacing it (sdk.ts:279-295).
	var modifiers []string
	if slices.ContainsFunc(tools, func(entry string) bool { return entry != "" && (entry[0] == '+' || entry[0] == '-') }) {
		modifiers = tools
	}
	selected := tools
	if modifiers != nil {
		selected = icodingagent.ApplyToolModifiers(defaultToolNames, modifiers)
	}
	var allowed []string
	switch {
	case modifiers != nil:
		if noTools == "all" {
			allowed = selected
		}
	case tools != nil:
		allowed = tools
	case noTools == "all":
		allowed = []string{}
	}
	var out sdkToolSelection
	out.modifiers = modifiers
	if allowed != nil {
		out.allowed = make(map[string]struct{}, len(allowed))
		for _, name := range allowed {
			out.allowed[name] = struct{}{}
		}
	}
	isExcluded := func(string) bool { return false }
	if excludeTools != nil {
		out.excluded = make(map[string]struct{}, len(excludeTools))
		for _, name := range excludeTools {
			out.excluded[name] = struct{}{}
		}
		isExcluded = extension.ToolNameMatcher(out.excluded)
	}
	initial := defaultToolNames
	if selected != nil {
		initial = selected
	}
	out.initialActive = slices.DeleteFunc(slices.Clone(initial), isExcluded)
	if out.initialActive == nil {
		out.initialActive = []string{}
	}
	return out
}

// resolveSDKModel is sdk.ts:190-232: the given model, else the session's saved model when its provider still has auth (a failed restore is
// reported), else the initial model of findInitialModel; with none, the no-models message.
func resolveSDKModel(services *AgentSessionServices, manager *SessionManager, given *ai.Model) (*ai.Model, string) {
	if given != nil {
		return given, ""
	}
	runtime := services.ModelRuntime()
	catalog := &sdkModelCatalog{models: runtime.RuntimeModels(), hasAuth: runtime.HasConfiguredAuth}
	branch := manager.GetBranch()
	existing := icodingagent.BuildSessionContext(branch, icodingagent.LastLeaf(), nil)
	hasExisting := len(existing.Messages) > 0
	var fallback string
	if hasExisting {
		// Assistant messages name the physical model that answered, so a virtual selection is only in model_change entries (sdk.ts:201-205).
		saved := GetBranchSelection(branch, func(provider, modelID string) *ai.Model { return runtime.GetModel(provider, modelID) })
		if saved != nil {
			if !saved.Unresolvable {
				if restored := runtime.GetModel(saved.Provider, saved.ModelID); restored != nil && runtime.HasConfiguredAuth(restored.ProviderMeta.ProviderID) {
					return restored, ""
				}
			}
			fallback = "Could not restore model " + saved.Provider + "/" + saved.ModelID
		}
	}
	settings := services.SettingsManager()
	result, err := icodingagent.FindInitialModel(icodingagent.FindInitialModelOptions{
		IsContinuing:         hasExisting,
		DefaultProvider:      settings.GetDefaultProvider(),
		DefaultModelId:       settings.GetDefaultModel(),
		DefaultThinkingLevel: string(settings.GetDefaultThinkingLevel()),
		ModelThinkingLevels:  thinkingLevelStrings(settings.GetAllModelThinkingLevels()),
		ModelRuntime:         catalog,
	})
	if err == nil && result.Model != nil {
		if model := runtime.GetModel(result.Model.Provider, result.Model.ID); model != nil {
			if fallback != "" {
				fallback += ". Using " + result.Model.Provider + "/" + result.Model.ID
			}
			return model, fallback
		}
	}
	return nil, icodingagent.FormatNoModelsAvailableMessage()
}

// sdkModelCatalog is the model-runtime surface findInitialModel reads: the composed models and per-provider configured auth.
type sdkModelCatalog struct {
	models  []icodingagent.RuntimeModel
	hasAuth func(providerID string) bool
}

func (c *sdkModelCatalog) GetModels(providerID string) []icodingagent.RuntimeModel {
	if providerID == "" {
		return slices.Clone(c.models)
	}
	return slices.DeleteFunc(slices.Clone(c.models), func(m icodingagent.RuntimeModel) bool { return m.Provider != providerID })
}

func (c *sdkModelCatalog) HasConfiguredAuth(providerID string) bool { return c.hasAuth(providerID) }

func (c *sdkModelCatalog) GetModel(providerID, modelID string) *icodingagent.RuntimeModel {
	if i := slices.IndexFunc(c.models, func(m icodingagent.RuntimeModel) bool { return m.Provider == providerID && m.ID == modelID }); i >= 0 {
		return &c.models[i]
	}
	return nil
}

func (c *sdkModelCatalog) GetAvailableSnapshot() []icodingagent.RuntimeModel {
	return slices.DeleteFunc(slices.Clone(c.models), func(m icodingagent.RuntimeModel) bool { return !c.hasAuth(m.Provider) })
}

func thinkingLevelStrings(levels map[string]ai.ThinkingLevel) map[string]string {
	out := make(map[string]string, len(levels))
	for key, level := range levels {
		out[key] = string(level)
	}
	return out
}
