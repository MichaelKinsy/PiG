package sdk

import (
	"errors"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ResourceCollision is Pi's ResourceCollision: two resources with one name.
type ResourceCollision struct {
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
	WinnerSource string `json:"winnerSource,omitempty"`
	LoserSource  string `json:"loserSource,omitempty"`
}

// ResourceDiagnostic is Pi's ResourceDiagnostic: a warning, error or collision found while resolving resources.
type ResourceDiagnostic struct {
	Type      string             `json:"type"`
	Message   string             `json:"message"`
	Path      string             `json:"path,omitempty"`
	Collision *ResourceCollision `json:"collision,omitempty"`
}

// Skill is Pi's Skill: a resolved skill.
type Skill struct {
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	FilePath               string     `json:"filePath"`
	BaseDir                string     `json:"baseDir"`
	SourceInfo             SourceInfo `json:"sourceInfo"`
	DisableModelInvocation bool       `json:"disableModelInvocation"`
}

// SkillsResult is what Pi's getSkills() returns and a skillsOverride takes and returns.
type SkillsResult struct {
	Skills      []Skill              `json:"skills"`
	Diagnostics []ResourceDiagnostic `json:"diagnostics"`
}

// PromptTemplate is Pi's PromptTemplate: a resolved prompt template.
type PromptTemplate struct {
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	ArgumentHint string     `json:"argumentHint,omitempty"`
	Content      string     `json:"content"`
	FilePath     string     `json:"filePath"`
	SourceInfo   SourceInfo `json:"sourceInfo"`
}

// PromptsResult is what Pi's getPrompts() returns and a promptsOverride takes and returns.
type PromptsResult struct {
	Prompts     []PromptTemplate     `json:"prompts"`
	Diagnostics []ResourceDiagnostic `json:"diagnostics"`
}

// ResourceTheme is the data of one of the themes Pi's getThemes() returns: its name and the file and source it came from. A theme is a rendering object the host builds from a file, so a themesOverride can keep or drop the loaded themes and cannot make one.
type ResourceTheme struct {
	Name       string      `json:"name"`
	SourcePath string      `json:"sourcePath,omitempty"`
	SourceInfo *SourceInfo `json:"sourceInfo,omitempty"`
}

// ThemesResult is what Pi's getThemes() returns and a themesOverride takes and returns.
type ThemesResult struct {
	Themes      []ResourceTheme      `json:"themes"`
	Diagnostics []ResourceDiagnostic `json:"diagnostics"`
}

// AgentsFile is one context file (AGENTS.md or CLAUDE.md) of Pi's getAgentsFiles().
type AgentsFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// AgentsFilesResult is what Pi's getAgentsFiles() returns and an agentsFilesOverride takes and returns.
type AgentsFilesResult struct {
	AgentsFiles []AgentsFile `json:"agentsFiles"`
}

// LoadedExtension is the data of one extension of Pi's LoadExtensionsResult: where it came from and the names of the tools it registered. The extension object itself holds functions that live in the host.
type LoadedExtension struct {
	Path         string     `json:"path"`
	ResolvedPath string     `json:"resolvedPath"`
	SourceInfo   SourceInfo `json:"sourceInfo"`
	Tools        []string   `json:"tools"`
}

// LoadExtensionsResult is Pi's LoadExtensionsResult as data: the extensions of a load with its errors and warnings.
type LoadExtensionsResult struct {
	Extensions []LoadedExtension `json:"extensions"`
	Errors     []struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	} `json:"errors"`
	Warnings []struct {
		Path    string `json:"path"`
		Warning string `json:"warning"`
	} `json:"warnings"`
}

// PathMetadata is Pi's PathMetadata: where a resource path came from.
type PathMetadata struct {
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}

// ExtensionResourcePath is one entry of Pi's ResourceExtensionPaths.
type ExtensionResourcePath struct {
	Path     string       `json:"path"`
	Metadata PathMetadata `json:"metadata"`
}

// ResourceExtensionPaths is what extendResources takes: the resources extensions contributed.
type ResourceExtensionPaths struct {
	SkillPaths  []ExtensionResourcePath `json:"skillPaths,omitempty"`
	PromptPaths []ExtensionResourcePath `json:"promptPaths,omitempty"`
	ThemePaths  []ExtensionResourcePath `json:"themePaths,omitempty"`
}

// ResourceSource is one {path} entry of Pi's getSystemPromptSource() and getAppendSystemPromptSources().
type ResourceSource struct {
	Path string `json:"path"`
}

// ResourceLoaderReloadOptions is Pi's ResourceLoaderReloadOptions. ResolveProjectTrust decides whether the project is trusted before any resource loads; it receives the bootstrap extension set as data.
type ResourceLoaderReloadOptions struct {
	ResolveProjectTrust func(LoadExtensionsResult) bool
}

// DefaultResourceLoaderOptions is Pi's DefaultResourceLoaderOptions. A nil pointer, nil slice or nil function is Pi's undefined.
//
// ExtensionFactories and ExtensionsOverride exist so code that sets them fails loudly instead of losing behavior: an inline factory runs in the extension process but the child session's extensions load in the host, and an override returns live extension objects. They are not supported yet (an owner decision is pending, gap "SdkSessionGraph" in test/parity/sdk-exports.toml); NewDefaultResourceLoader returns an error when either is set. AdditionalExtensionPaths loads extension sources, and CreateAgentSession's custom tools serve tools from the parent extension.
type DefaultResourceLoaderOptions struct {
	Cwd                           string
	AgentDir                      string
	SettingsManager               *SettingsManager
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	AdditionalThemePaths          []string
	ExtensionFactories            []any
	NoExtensions                  bool
	DisabledBuiltinExtensions     []string
	NoSkills                      bool
	NoPromptTemplates             bool
	NoThemes                      bool
	NoContextFiles                bool
	SystemPrompt                  *string
	AppendSystemPrompt            []string
	ExtensionsOverride            func(LoadExtensionsResult) LoadExtensionsResult
	SkillsOverride                func(SkillsResult) SkillsResult
	PromptsOverride               func(PromptsResult) PromptsResult
	ThemesOverride                func(ThemesResult) ThemesResult
	AgentsFilesOverride           func(AgentsFilesResult) AgentsFilesResult
	SystemPromptOverride          func(*string) *string
	AppendSystemPromptOverride    func([]string) []string
}

// DefaultResourceLoader is Pi's `DefaultResourceLoader` class (core/resource-loader.ts): the skills, prompt templates, themes, context files, system prompt and extensions of a project and agent directory. The host owns the loader; a loader passed to CreateAgentSession is the very loader the child session reads. Call Reload before reading, as Pi requires.
type DefaultResourceLoader struct{ object *graphObject }

// graphFunc wraps a typed override as a function the host runs with JSON arguments.
func graphFunc[In, Out any](override func(In) Out) graphCallback {
	if override == nil {
		return nil
	}
	return func(args []json.RawMessage) (any, error) {
		var input In
		if len(args) > 0 {
			if err := json.Unmarshal(args[0], &input); err != nil {
				return nil, err
			}
		}
		return override(input), nil
	}
}

// NewDefaultResourceLoader is `new DefaultResourceLoader(options)`.
func NewDefaultResourceLoader(c Context, options DefaultResourceLoaderOptions) (*DefaultResourceLoader, error) {
	if len(options.ExtensionFactories) > 0 {
		return nil, errors.New("DefaultResourceLoader option extensionFactories is not supported by the Go SDK: an inline factory runs in this process but the child session's extensions load in the host (decision pending, gap SdkSessionGraph); use AdditionalExtensionPaths, or CreateAgentSession CustomTools")
	}
	if options.ExtensionsOverride != nil {
		return nil, errors.New("DefaultResourceLoader option extensionsOverride is not supported by the Go SDK: it returns live extension objects, which live in the host (decision pending, gap SdkSessionGraph)")
	}
	wire := map[string]any{
		"cwd": options.Cwd, "agentDir": options.AgentDir,
		"additionalExtensionPaths": options.AdditionalExtensionPaths, "additionalSkillPaths": options.AdditionalSkillPaths,
		"additionalPromptTemplatePaths": options.AdditionalPromptTemplatePaths, "additionalThemePaths": options.AdditionalThemePaths,
		"noExtensions": options.NoExtensions, "noSkills": options.NoSkills, "noPromptTemplates": options.NoPromptTemplates, "noThemes": options.NoThemes, "noContextFiles": options.NoContextFiles,
		"disabledBuiltinExtensions": options.DisabledBuiltinExtensions, "systemPrompt": options.SystemPrompt, "appendSystemPrompt": options.AppendSystemPrompt,
		"skillsOverride":             c.callbackID(graphFunc(options.SkillsOverride)),
		"promptsOverride":            c.callbackID(graphFunc(options.PromptsOverride)),
		"themesOverride":             c.callbackID(graphFunc(options.ThemesOverride)),
		"agentsFilesOverride":        c.callbackID(graphFunc(options.AgentsFilesOverride)),
		"systemPromptOverride":       c.callbackID(graphFunc(options.SystemPromptOverride)),
		"appendSystemPromptOverride": c.callbackID(graphFunc(options.AppendSystemPromptOverride)),
	}
	if options.SettingsManager != nil {
		wire["settingsManager"] = options.SettingsManager.Handle()
	}
	object, err := newGraphObject(c, "DefaultResourceLoader", "new", wire)
	if err != nil {
		return nil, err
	}
	return &DefaultResourceLoader{object: object}, nil
}

// Handle is the host's name for the loader, which CreateAgentSession puts on the wire.
func (l *DefaultResourceLoader) Handle() string { return l.object.Handle() }

// GetExtensions is the extensions of the last Reload, as data.
func (l *DefaultResourceLoader) GetExtensions() (LoadExtensionsResult, error) {
	return graphMethodCall[LoadExtensionsResult](l.object, "getExtensions")
}

// GetSkills is the skills of the last Reload.
func (l *DefaultResourceLoader) GetSkills() (SkillsResult, error) {
	return graphMethodCall[SkillsResult](l.object, "getSkills")
}

// GetPrompts is the prompt templates of the last Reload.
func (l *DefaultResourceLoader) GetPrompts() (PromptsResult, error) {
	return graphMethodCall[PromptsResult](l.object, "getPrompts")
}

// GetThemes is the themes of the last Reload.
func (l *DefaultResourceLoader) GetThemes() (ThemesResult, error) {
	return graphMethodCall[ThemesResult](l.object, "getThemes")
}

// GetAgentsFiles is the context files of the last Reload.
func (l *DefaultResourceLoader) GetAgentsFiles() (AgentsFilesResult, error) {
	return graphMethodCall[AgentsFilesResult](l.object, "getAgentsFiles")
}

// GetSystemPrompt is the system prompt that replaces the default one, nil when there is none.
func (l *DefaultResourceLoader) GetSystemPrompt() (*string, error) {
	return graphMethodCall[*string](l.object, "getSystemPrompt")
}

// GetSystemPromptSource is the file the system prompt came from, nil when it did not come from one.
func (l *DefaultResourceLoader) GetSystemPromptSource() (*ResourceSource, error) {
	return graphMethodCall[*ResourceSource](l.object, "getSystemPromptSource")
}

// GetAppendSystemPrompt is the texts appended to the system prompt.
func (l *DefaultResourceLoader) GetAppendSystemPrompt() ([]string, error) {
	return graphMethodCall[[]string](l.object, "getAppendSystemPrompt")
}

// GetAppendSystemPromptSources is the files the appended texts came from.
func (l *DefaultResourceLoader) GetAppendSystemPromptSources() ([]ResourceSource, error) {
	return graphMethodCall[[]ResourceSource](l.object, "getAppendSystemPromptSources")
}

// ExtendResources adds the skills, prompt templates and themes extensions contributed.
func (l *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) error {
	_, err := graphMethodCall[graphDone](l.object, "extendResources", paths)
	return err
}

// LoadProjectTrustExtensions loads the bootstrap extension set, with project settings untrusted, that project trust is decided with.
func (l *DefaultResourceLoader) LoadProjectTrustExtensions() (LoadExtensionsResult, error) {
	return graphMethodAsync[LoadExtensionsResult](l.object, "loadProjectTrustExtensions")
}

// Reload rediscovers every resource. A nil options is Pi's undefined.
func (l *DefaultResourceLoader) Reload(options *ResourceLoaderReloadOptions) error {
	wire := map[string]any{}
	if options != nil && options.ResolveProjectTrust != nil {
		wire["resolveProjectTrust"] = l.object.context.callbackID(graphFunc(options.ResolveProjectTrust))
	}
	_, err := graphMethodAsync[graphDone](l.object, "reload", wire)
	return err
}
