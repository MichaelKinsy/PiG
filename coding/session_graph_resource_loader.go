// pig-specific: serves Pi's DefaultResourceLoader (packages/coding-agent/src/core/resource-loader.ts) to the Go, Python and Rust extension SDKs as a host-owned object an extension names by handle. The object is the runtime's own *DefaultResourceLoader, so a session created from it reads the resources it loaded. The Node runtime serves Pi's own class in the extension process.

package coding

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// GraphResourceLoader is a DefaultResourceLoader an extension constructed.
type GraphResourceLoader struct {
	loader *DefaultResourceLoader
	scope  subprocess.SessionGraphScope

	// callbackErr is the first error an override function of the extension returned during the current call; Pi's override throwing rejects reload().
	mu          sync.Mutex
	callbackErr error
}

// Loader is the loader the handle names. A session created from it shares it, as Pi's createAgentSession shares the ResourceLoader it is given.
func (g *GraphResourceLoader) Loader() *DefaultResourceLoader { return g.loader }

// Release implements subprocess.SessionGraphReleaser: when the last holder lets go (the extension's handle and every session created over the loader), the Extension Host the loader started for its extensions shuts down. Pi's extensions live in the importing process and need no close; here they are processes.
func (g *GraphResourceLoader) Release() { g.loader.Close() }

// graphLoaderOptions is Pi's DefaultResourceLoaderOptions as it crosses the wire: an object option is the handle of the object, a function option is the name of the function the extension holds.
type graphLoaderOptions struct {
	Cwd                           string    `json:"cwd"`
	AgentDir                      string    `json:"agentDir"`
	SettingsManager               string    `json:"settingsManager"`
	AdditionalExtensionPaths      []string  `json:"additionalExtensionPaths"`
	AdditionalSkillPaths          []string  `json:"additionalSkillPaths"`
	AdditionalPromptTemplatePaths []string  `json:"additionalPromptTemplatePaths"`
	AdditionalThemePaths          []string  `json:"additionalThemePaths"`
	NoExtensions                  bool      `json:"noExtensions"`
	NoSkills                      bool      `json:"noSkills"`
	NoPromptTemplates             bool      `json:"noPromptTemplates"`
	NoThemes                      bool      `json:"noThemes"`
	NoContextFiles                bool      `json:"noContextFiles"`
	SystemPrompt                  *string   `json:"systemPrompt"`
	AppendSystemPrompt            *[]string `json:"appendSystemPrompt"`
	DisabledBuiltinExtensions     []string  `json:"disabledBuiltinExtensions"`
	SkillsOverride                string    `json:"skillsOverride"`
	PromptsOverride               string    `json:"promptsOverride"`
	ThemesOverride                string    `json:"themesOverride"`
	AgentsFilesOverride           string    `json:"agentsFilesOverride"`
	SystemPromptOverride          string    `json:"systemPromptOverride"`
	AppendSystemPromptOverride    string    `json:"appendSystemPromptOverride"`
}

func newGraphResourceLoader(scope subprocess.SessionGraphScope, op string, raw json.RawMessage) (*GraphResourceLoader, error) {
	if op != "new" {
		return nil, fmt.Errorf("DefaultResourceLoader has no static factory %q", op)
	}
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	var options graphLoaderOptions
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return nil, err
	}
	g := &GraphResourceLoader{scope: scope}
	loaderOptions := DefaultResourceLoaderOptions{
		CWD: options.Cwd, AgentDir: options.AgentDir,
		AdditionalExtensionPaths: options.AdditionalExtensionPaths, AdditionalSkillPaths: options.AdditionalSkillPaths,
		AdditionalPromptTemplatePaths: options.AdditionalPromptTemplatePaths, AdditionalThemePaths: options.AdditionalThemePaths,
		NoExtensions: options.NoExtensions, NoSkills: options.NoSkills, NoPromptTemplates: options.NoPromptTemplates, NoThemes: options.NoThemes, NoContextFiles: options.NoContextFiles,
		SystemPrompt: options.SystemPrompt, DisabledBuiltinExtensions: options.DisabledBuiltinExtensions,
	}
	if options.AppendSystemPrompt != nil {
		loaderOptions.AppendSystemPrompt = append([]string{}, *options.AppendSystemPrompt...)
	}
	if options.SettingsManager != "" {
		object, ok := scope.Object(options.SettingsManager)
		settings, isSettings := object.(*icodingagent.GraphSettingsManager)
		if !ok || !isSettings {
			return nil, errors.New(subprocess.SessionGraphUnknownMessage)
		}
		loaderOptions.SettingsManager = settings.Manager()
	}
	if id := options.SkillsOverride; id != "" {
		loaderOptions.SkillsOverride = func(base SkillsResult) SkillsResult {
			var out graphSkills
			if raw, ok := g.override(id, graphSkillsOf(base)); ok && g.decoded(json.Unmarshal(raw, &out)) {
				return out.skills(base)
			}
			return base
		}
	}
	if id := options.PromptsOverride; id != "" {
		loaderOptions.PromptsOverride = func(base PromptsResult) PromptsResult {
			var out graphPrompts
			if raw, ok := g.override(id, graphPromptsOf(base)); ok && g.decoded(json.Unmarshal(raw, &out)) {
				return out.prompts(base)
			}
			return base
		}
	}
	if id := options.ThemesOverride; id != "" {
		loaderOptions.ThemesOverride = func(base ThemesResult) ThemesResult {
			var out graphThemes
			if raw, ok := g.override(id, graphThemesOf(base)); ok && g.decoded(json.Unmarshal(raw, &out)) {
				return out.themes(base)
			}
			return base
		}
	}
	if id := options.AgentsFilesOverride; id != "" {
		loaderOptions.AgentsFilesOverride = func(base AgentsFilesResult) AgentsFilesResult {
			var out struct {
				AgentsFiles []struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				} `json:"agentsFiles"`
			}
			raw, ok := g.override(id, graphAgentsFilesOf(base))
			if !ok || !g.decoded(json.Unmarshal(raw, &out)) {
				return base
			}
			files := make([]ContextFile, len(out.AgentsFiles))
			for i, file := range out.AgentsFiles {
				files[i] = ContextFile{Path: file.Path, Content: file.Content}
			}
			return AgentsFilesResult{AgentsFiles: files}
		}
	}
	if id := options.SystemPromptOverride; id != "" {
		loaderOptions.SystemPromptOverride = func(base *string) *string {
			var out *string
			if raw, ok := g.override(id, base); ok && g.decoded(json.Unmarshal(raw, &out)) {
				return out
			}
			return base
		}
	}
	if id := options.AppendSystemPromptOverride; id != "" {
		loaderOptions.AppendSystemPromptOverride = func(base []string) []string {
			var out []string
			if raw, ok := g.override(id, base); ok && g.decoded(json.Unmarshal(raw, &out)) {
				return out
			}
			return base
		}
	}
	g.loader = NewDefaultResourceLoader(loaderOptions)
	return g, nil
}

// override runs the extension's function id with base and returns its result. It reports false, and keeps the error for the call that is running, when the function failed.
func (g *GraphResourceLoader) override(id string, base any) (json.RawMessage, bool) {
	input, err := jsstring.AppendMarshalJSON([]byte{'['}, base)
	if err == nil {
		var result json.RawMessage
		//portlint:allow ctxbackground Pi's override options (agentsFilesOverride, systemPromptOverride and the rest) are synchronous functions of the loader's load, which carries no signal
		if result, err = g.scope.Callback(context.Background(), id, json.RawMessage(append(input, ']'))); err == nil {
			return result, true
		}
	}
	g.keepCallbackErr(err)
	return nil, false
}

// decoded reports whether err, the error of decoding an override's result, is nil, and keeps it for the call that is running otherwise.
func (g *GraphResourceLoader) decoded(err error) bool {
	if err == nil {
		return true
	}
	g.keepCallbackErr(err)
	return false
}

func (g *GraphResourceLoader) keepCallbackErr(err error) {
	g.mu.Lock()
	if g.callbackErr == nil {
		g.callbackErr = err
	}
	g.mu.Unlock()
}

func (g *GraphResourceLoader) takeCallbackErr() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := g.callbackErr
	g.callbackErr = nil
	return err
}

// The JSON shapes of Pi's loader results. Pi's Skill, PromptTemplate and Theme are class or interface values; an extension receives their data members.

type graphSkill struct {
	Name                   string               `json:"name"`
	Description            string               `json:"description"`
	FilePath               string               `json:"filePath"`
	BaseDir                string               `json:"baseDir"`
	SourceInfo             extension.SourceInfo `json:"sourceInfo"`
	DisableModelInvocation bool                 `json:"disableModelInvocation"`
}

type graphSkills struct {
	Skills      []graphSkill                   `json:"skills"`
	Diagnostics []extension.ResourceDiagnostic `json:"diagnostics"`
}

func diagnosticsOf(list []extension.ResourceDiagnostic) []extension.ResourceDiagnostic {
	if list == nil {
		return []extension.ResourceDiagnostic{}
	}
	return list
}

func graphSkillsOf(result SkillsResult) graphSkills {
	out := graphSkills{Skills: []graphSkill{}, Diagnostics: diagnosticsOf(result.Diagnostics)}
	for _, skill := range result.Skills {
		out.Skills = append(out.Skills, graphSkill{skill.Name, skill.Description, skill.FilePath, skill.BaseDir, skill.SourceInfo, skill.DisableModelInvocation})
	}
	return out
}

// skills is the result an override returned: a skill it kept is the loader's own, matched by file, and one it made is built from its data.
func (s graphSkills) skills(base SkillsResult) SkillsResult {
	known := map[string]*Skill{}
	for _, skill := range base.Skills {
		known[skill.FilePath] = skill
	}
	out := SkillsResult{Diagnostics: s.Diagnostics}
	for _, skill := range s.Skills {
		if original := known[skill.FilePath]; original != nil && original.Name == skill.Name && original.Description == skill.Description && original.DisableModelInvocation == skill.DisableModelInvocation {
			out.Skills = append(out.Skills, original)
			continue
		}
		out.Skills = append(out.Skills, &Skill{Name: skill.Name, Description: skill.Description, FilePath: skill.FilePath, BaseDir: skill.BaseDir, SourceInfo: skill.SourceInfo, DisableModelInvocation: skill.DisableModelInvocation})
	}
	return out
}

type graphPrompt struct {
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	ArgumentHint string               `json:"argumentHint,omitempty"`
	Content      string               `json:"content"`
	FilePath     string               `json:"filePath"`
	SourceInfo   extension.SourceInfo `json:"sourceInfo"`
}

type graphPrompts struct {
	Prompts     []graphPrompt                  `json:"prompts"`
	Diagnostics []extension.ResourceDiagnostic `json:"diagnostics"`
}

func graphPromptsOf(result PromptsResult) graphPrompts {
	out := graphPrompts{Prompts: []graphPrompt{}, Diagnostics: diagnosticsOf(result.Diagnostics)}
	for _, prompt := range result.Prompts {
		out.Prompts = append(out.Prompts, graphPrompt{prompt.Name, prompt.Description, prompt.ArgumentHint, prompt.Content, prompt.FilePath, prompt.SourceInfo})
	}
	return out
}

func (p graphPrompts) prompts(base PromptsResult) PromptsResult {
	known := map[string]PromptTemplate{}
	for _, prompt := range base.Prompts {
		known[prompt.FilePath] = prompt
	}
	out := PromptsResult{Diagnostics: p.Diagnostics}
	for _, prompt := range p.Prompts {
		template := known[prompt.FilePath]
		template.Name, template.Description, template.ArgumentHint, template.Content, template.FilePath, template.SourceInfo = prompt.Name, prompt.Description, prompt.ArgumentHint, prompt.Content, prompt.FilePath, prompt.SourceInfo
		out.Prompts = append(out.Prompts, template)
	}
	return out
}

type graphTheme struct {
	Name       string                `json:"name"`
	SourcePath string                `json:"sourcePath,omitempty"`
	SourceInfo *extension.SourceInfo `json:"sourceInfo,omitempty"`
}

type graphThemes struct {
	Themes      []graphTheme                   `json:"themes"`
	Diagnostics []extension.ResourceDiagnostic `json:"diagnostics"`
}

func graphThemesOf(result ThemesResult) graphThemes {
	out := graphThemes{Themes: []graphTheme{}, Diagnostics: diagnosticsOf(result.Diagnostics)}
	for _, theme := range result.Themes {
		item := graphTheme{Name: theme.Name, SourcePath: theme.SourcePath}
		if theme.SourceInfo != nil {
			info := extension.SourceInfo(*theme.SourceInfo)
			item.SourceInfo = &info
		}
		out.Themes = append(out.Themes, item)
	}
	return out
}

// themes is the result an override returned. A theme is a rendering object the host builds from a file, so an override can keep or drop the loaded themes, by name and file, and cannot make one.
func (t graphThemes) themes(base ThemesResult) ThemesResult {
	out := ThemesResult{Diagnostics: t.Diagnostics}
	for _, theme := range t.Themes {
		for _, original := range base.Themes {
			if original.Name == theme.Name && original.SourcePath == theme.SourcePath {
				out.Themes = append(out.Themes, original)
				break
			}
		}
	}
	return out
}

func graphAgentsFilesOf(result AgentsFilesResult) map[string]any {
	files := make([]map[string]string, 0, len(result.AgentsFiles))
	for _, file := range result.AgentsFiles {
		files = append(files, map[string]string{"path": file.Path, "content": file.Content})
	}
	return map[string]any{"agentsFiles": files}
}

// graphExtensionsOf is Pi's LoadExtensionsResult as data: each extension's path, resolved path, source and tool names, and the load's errors and warnings. The extension objects themselves hold functions that live in the host.
func graphExtensionsOf(result LoadExtensionsResult) map[string]any {
	extensions := make([]map[string]any, 0, len(result.Extensions))
	for _, ext := range result.Extensions {
		tools := append([]string{}, ext.ToolOrder...)
		extensions = append(extensions, map[string]any{"path": ext.Path, "resolvedPath": ext.ResolvedPath, "sourceInfo": ext.SourceInfo, "tools": tools})
	}
	errs := make([]map[string]string, 0, len(result.Errors))
	for _, loadErr := range result.Errors {
		errs = append(errs, map[string]string{"path": loadErr.Path, "error": loadErr.Error})
	}
	warnings := make([]map[string]string, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warnings = append(warnings, map[string]string{"path": warning.Path, "warning": warning.Warning})
	}
	return map[string]any{"extensions": extensions, "errors": errs, "warnings": warnings}
}

// Call runs a DefaultResourceLoader method.
func (g *GraphResourceLoader) Call(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := icodingagent.ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	loader := g.loader
	switch method {
	case "getExtensions":
		return graphExtensionsOf(loader.GetExtensions()), nil
	case "getSkills":
		return graphSkillsOf(loader.GetSkills()), nil
	case "getPrompts":
		return graphPromptsOf(loader.GetPrompts()), nil
	case "getThemes":
		return graphThemesOf(loader.GetThemes()), nil
	case "getAgentsFiles":
		return graphAgentsFilesOf(loader.GetAgentsFiles()), nil
	case "getSystemPrompt":
		if prompt, ok := loader.GetSystemPrompt(); ok {
			return prompt, nil
		}
		return nil, nil
	case "getSystemPromptSource":
		if source, ok := loader.GetSystemPromptSource(); ok {
			return map[string]string{"path": source.Path}, nil
		}
		return nil, nil
	case "getAppendSystemPrompt":
		return append([]string{}, loader.GetAppendSystemPrompt()...), nil
	case "getAppendSystemPromptSources":
		sources := []map[string]string{}
		for _, source := range loader.GetAppendSystemPromptSources() {
			sources = append(sources, map[string]string{"path": source.Path})
		}
		return sources, nil
	case "extendResources":
		return nil, g.extendResources(args)
	case "reload":
		return nil, g.reload(ctx, args)
	case "loadProjectTrustExtensions":
		result, err := loader.LoadProjectTrustExtensions(ctx)
		if err != nil {
			return nil, err
		}
		return graphExtensionsOf(result), nil
	}
	return nil, errors.New("DefaultResourceLoader has no method " + method)
}

// extendResources is `extendResources({skillPaths?, promptPaths?, themePaths?})`.
func (g *GraphResourceLoader) extendResources(args icodingagent.GraphArgs) error {
	var paths struct {
		SkillPaths  []graphResourcePath `json:"skillPaths"`
		PromptPaths []graphResourcePath `json:"promptPaths"`
		ThemePaths  []graphResourcePath `json:"themePaths"`
	}
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &paths) }); err != nil {
		return err
	}
	convert := func(list []graphResourcePath) []ExtensionResourcePath {
		out := make([]ExtensionResourcePath, len(list))
		for i, item := range list {
			out[i] = ExtensionResourcePath{Path: item.Path, Metadata: icodingagent.PathMetadata{Source: item.Metadata.Source, Scope: item.Metadata.Scope, Origin: item.Metadata.Origin, BaseDir: item.Metadata.BaseDir}}
		}
		return out
	}
	err := g.loader.ExtendResources(ResourceExtensionPaths{SkillPaths: convert(paths.SkillPaths), PromptPaths: convert(paths.PromptPaths), ThemePaths: convert(paths.ThemePaths)})
	if err != nil {
		return err
	}
	return g.takeCallbackErr()
}

type graphResourcePath struct {
	Path     string `json:"path"`
	Metadata struct {
		Source  string `json:"source"`
		Scope   string `json:"scope"`
		Origin  string `json:"origin"`
		BaseDir string `json:"baseDir"`
	} `json:"metadata"`
}

// reload is `reload(options?)`; options.resolveProjectTrust is the name of the extension's function.
func (g *GraphResourceLoader) reload(ctx context.Context, args icodingagent.GraphArgs) error {
	var options struct {
		ResolveProjectTrust string `json:"resolveProjectTrust"`
	}
	if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &options) }); err != nil {
		return err
	}
	var reloadOptions []ResourceLoaderReloadOptions
	if id := options.ResolveProjectTrust; id != "" {
		reloadOptions = append(reloadOptions, ResourceLoaderReloadOptions{ResolveProjectTrust: func(callCtx context.Context, input ResolveProjectTrustInput) (bool, error) {
			//portlint:allow mapkeyorder graphExtensionsOf is a data view PiG defines (Pi's extension objects hold functions), which the extension reads by member name
			encoded, err := jsstring.AppendMarshalJSON([]byte{'['}, graphExtensionsOf(input.ExtensionsResult))
			if err != nil {
				return false, err
			}
			result, err := g.scope.Callback(callCtx, id, json.RawMessage(append(encoded, ']')))
			if err != nil {
				return false, err
			}
			var trusted bool
			if err := json.Unmarshal(result, &trusted); err != nil {
				return false, fmt.Errorf("resolveProjectTrust returned %s, not a boolean", result)
			}
			return trusted, nil
		}})
	}
	if err := g.loader.ReloadWith(ctx, reloadOptions...); err != nil {
		return err
	}
	return g.takeCallbackErr()
}

var _ subprocess.SessionGraphObject = (*GraphResourceLoader)(nil)
var _ subprocess.SessionGraphReleaser = (*GraphResourceLoader)(nil)
