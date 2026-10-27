package sdk

import "path/filepath"

// Pi's createAgentSessionServices and createAgentSessionFromServices (core/agent-session-services.ts). The function builds the objects of one session cwd as Pi's does (ModelRuntime.create, SettingsManager.create, new DefaultResourceLoader and its reload),
// then asks the host for the two steps that need its state: the resolved paths, and the registrations and extension flag values of the extensions the loader loaded (agent-session-services.ts:138-139 and :157-193).

// AgentSessionRuntimeDiagnostic is Pi's AgentSessionRuntimeDiagnostic: a non-fatal issue found while creating services or a session. Type is "info", "warning" or "error".
type AgentSessionRuntimeDiagnostic struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ExtensionFlagValue is one entry of Pi's `extensionFlagValues` Map<string, boolean | string>: the value of --Name, true for a boolean flag or a string for a string flag. The list keeps command-line order, which shows in the diagnostics.
type ExtensionFlagValue struct {
	Name  string
	Value any
}

// CreateAgentSessionServicesOptions is Pi's CreateAgentSessionServicesOptions. A zero member is Pi's undefined. The Cwd, AgentDir and SettingsManager of ResourceLoaderOptions are the services' own, as in Pi. Pi's `modelRuntimeSignal` is the
// call itself: a cancelled request ends the creation, as it does ModelRuntimeCreate.
type CreateAgentSessionServicesOptions struct {
	Cwd                         string
	AgentDir                    string
	SettingsManager             *SettingsManager
	ModelRuntime                *ModelRuntime
	ExtensionFlagValues         []ExtensionFlagValue
	ResourceLoaderOptions       *DefaultResourceLoaderOptions
	ResourceLoaderReloadOptions *ResourceLoaderReloadOptions
}

// AgentSessionServices is Pi's AgentSessionServices: the coherent cwd-bound services of one session cwd. The session itself is created separately ([CreateAgentSessionFromServices]).
type AgentSessionServices struct {
	Cwd             string
	AgentDir        string
	ModelRuntime    *ModelRuntime
	SettingsManager *SettingsManager
	ResourceLoader  *DefaultResourceLoader
	Diagnostics     []AgentSessionRuntimeDiagnostic
}

// CreateAgentSessionServices is `createAgentSessionServices(options)`: cwd-bound services plus the diagnostics found while creating them. It does not create an AgentSession.
func CreateAgentSessionServices(c Context, options CreateAgentSessionServicesOptions) (*AgentSessionServices, error) {
	var agentDir *string
	if options.AgentDir != "" {
		agentDir = &options.AgentDir
	}
	paths, err := graphStatic[struct {
		Cwd      string `json:"cwd"`
		AgentDir string `json:"agentDir"`
	}](c, "AgentSessionServices", "resolve", options.Cwd, agentDir)
	if err != nil {
		return nil, err
	}
	modelRuntime := options.ModelRuntime
	if modelRuntime == nil {
		modelsPath := filepath.Join(paths.AgentDir, "models.json")
		if modelRuntime, err = ModelRuntimeCreate(c, ModelRuntimeCreateOptions{AuthPath: filepath.Join(paths.AgentDir, "auth.json"), ModelsPath: &modelsPath}); err != nil {
			return nil, err
		}
	}
	settingsManager := options.SettingsManager
	if settingsManager == nil {
		created, err := SettingsManagerCreate(c, paths.Cwd, &paths.AgentDir, nil)
		if err != nil {
			return nil, err
		}
		settingsManager = &created
	}
	var loaderOptions DefaultResourceLoaderOptions
	if options.ResourceLoaderOptions != nil {
		loaderOptions = *options.ResourceLoaderOptions
	}
	loaderOptions.Cwd, loaderOptions.AgentDir, loaderOptions.SettingsManager = paths.Cwd, paths.AgentDir, settingsManager
	resourceLoader, err := NewDefaultResourceLoader(c, loaderOptions)
	if err != nil {
		return nil, err
	}
	if err := resourceLoader.Reload(options.ResourceLoaderReloadOptions); err != nil {
		return nil, err
	}
	flags := make([][2]any, len(options.ExtensionFlagValues))
	for i, flag := range options.ExtensionFlagValues {
		flags[i] = [2]any{flag.Name, flag.Value}
	}
	diagnostics, err := graphStatic[[]AgentSessionRuntimeDiagnostic](c, "AgentSessionServices", "apply", modelRuntime.Handle(), settingsManager.Handle(), resourceLoader.Handle(), paths.Cwd, paths.AgentDir, flags)
	if err != nil {
		return nil, err
	}
	if diagnostics == nil {
		diagnostics = []AgentSessionRuntimeDiagnostic{}
	}
	return &AgentSessionServices{Cwd: paths.Cwd, AgentDir: paths.AgentDir, ModelRuntime: modelRuntime, SettingsManager: settingsManager, ResourceLoader: resourceLoader, Diagnostics: diagnostics}, nil
}

// CreateAgentSessionFromServicesOptions is Pi's CreateAgentSessionFromServicesOptions. A zero member is Pi's undefined.
type CreateAgentSessionFromServicesOptions struct {
	Services          *AgentSessionServices
	SessionManager    *SessionManager
	SessionStartEvent *SessionStartEvent
	Model             any
	ThinkingLevel     string
	ScopedModels      []ScopedModel
	Tools             []string
	ExcludeTools      []string
	NoTools           string
	CustomTools       []ToolDefinition
}

// CreateAgentSessionFromServices is `createAgentSessionFromServices(options)`: an AgentSession over previously created services, which keeps session creation separate so the caller can resolve the model, thinking level and tools against the target cwd first.
func CreateAgentSessionFromServices(c Context, options CreateAgentSessionFromServicesOptions) (*AgentSession, error) {
	services := options.Services
	return c.CreateAgentSession(CreateAgentSessionOptions{
		Cwd: services.Cwd, AgentDir: services.AgentDir, ModelRuntime: services.ModelRuntime, SettingsManager: services.SettingsManager, ResourceLoader: services.ResourceLoader,
		SessionManager: options.SessionManager, Model: options.Model, ThinkingLevel: options.ThinkingLevel, ScopedModels: options.ScopedModels, Tools: options.Tools,
		ExcludeTools: options.ExcludeTools, NoTools: options.NoTools, CustomTools: options.CustomTools, SessionStartEvent: options.SessionStartEvent,
	})
}
