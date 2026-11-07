package cli

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/ctxowner"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// cliResourceLoader is the resource loader of a print, JSON or RPC Session: the prompt templates and skills of its build, each carrying the sourceInfo command discovery reports for it, the resources its extensions contribute, the build's system prompt inputs and context files, and its extensions (resource-loader.ts DefaultResourceLoader). Its other methods answer as [coding.NoResources] does.
type cliResourceLoader struct {
	coding.ResourceLoader

	cwd      string
	agentDir string

	// reload rereads the build's resources and reloads its extensions under ctx; it reports the build's prompt templates, skills and recorded source info, and its extensions with their runtime.
	reload func(ctx context.Context) (cliReloadedResources, error)

	mu         sync.Mutex
	prompt     resolvedPromptInputs
	agents     []codingagent.ContextFile
	extensions coding.LoadExtensionsResult
	prompts    []codingagent.PromptTemplate
	skills     []*codingagent.SkillDef
	sourceInfo map[string]codingagent.ResourceSourceInfo
}

func newCLIResourceLoader(cwd, agentDir string) *cliResourceLoader {
	return &cliResourceLoader{ResourceLoader: coding.NoResources, cwd: cwd, agentDir: agentDir}
}

// buildResourceLoader returns the resource loader of a Session started from build (resource-loader.ts DefaultResourceLoader over the build's resolved resources). owner is the process context the extension processes a reload starts live under; the reload keeps the request values of the call that asked for it.
func (b *cliRuntimeBuilder) buildResourceLoader(owner context.Context, build *cliBuild) *cliResourceLoader {
	sourceInfo := func() map[string]codingagent.ResourceSourceInfo {
		if build.RPCSourceInfo != nil {
			return build.RPCSourceInfo
		}
		return build.SkillCatalog.SourceInfo
	}
	skills := func() []*codingagent.SkillDef {
		return build.SkillCatalog.WithSkillSources(rpcResolvedSkills(build.skills(), b.activePiglet))
	}
	loader := newCLIResourceLoader(build.CWD, b.agentDir)
	loader.set(codingagent.LoadPromptTemplates("", "", build.PromptPaths...).Templates, skills(), sourceInfo())
	loader.setPrompt(build.ResolvedPrompts, build.ContextFiles)
	loader.extensions = coding.LoadExtensionsResult{Extensions: build.Extensions, Runtime: build.Runtime}
	loader.reload = func(ctx context.Context) (cliReloadedResources, error) {
		if err := b.reloadBuildResources(build); err != nil {
			return cliReloadedResources{}, err
		}
		extensions, err := b.reloadBuildExtensions(ctxowner.WithValuesOf(owner, ctx), build)
		if err != nil {
			return cliReloadedResources{}, err
		}
		return cliReloadedResources{
			prompt:     build.ResolvedPrompts,
			agents:     build.ContextFiles,
			prompts:    codingagent.LoadPromptTemplates("", "", build.PromptPaths...).Templates,
			skills:     skills(),
			sourceInfo: sourceInfo(),
			extensions: coding.LoadExtensionsResult{Extensions: extensions, Runtime: build.Runtime},
		}, nil
	}
	return loader
}

// set replaces the loader's resources with a build's: its prompt templates and skills, and the source info its resolver recorded by path.
func (l *cliResourceLoader) set(prompts []codingagent.PromptTemplate, skills []*codingagent.SkillDef, sourceInfo map[string]codingagent.ResourceSourceInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sourceInfo = sourceInfo
	catalog := l.catalog()
	l.prompts = catalog.WithPromptSources(prompts)
	l.skills = catalog.WithSkillCommandSources(skills)
}

// cliReloadedResources are a build's resources after a reload.
type cliReloadedResources struct {
	prompt     resolvedPromptInputs
	agents     []codingagent.ContextFile
	prompts    []codingagent.PromptTemplate
	skills     []*codingagent.SkillDef
	sourceInfo map[string]codingagent.ResourceSourceInfo
	extensions coding.LoadExtensionsResult
}

// setPrompt replaces the build's system prompt inputs: its custom and appended prompt text and its context files.
func (l *cliResourceLoader) setPrompt(prompt resolvedPromptInputs, agents []codingagent.ContextFile) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prompt = prompt
	l.agents = slices.Clone(agents)
}

func (l *cliResourceLoader) GetAgentsFiles() coding.AgentsFilesResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	return coding.AgentsFilesResult{AgentsFiles: slices.Clone(l.agents)}
}

func (l *cliResourceLoader) GetSystemPrompt() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prompt.custom, l.prompt.customSet
}

func (l *cliResourceLoader) GetAppendSystemPrompt() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prompt.append == "" {
		return nil
	}
	return []string{l.prompt.append}
}

// GetExtensions returns the extensions the last reload loaded (resource-loader.ts getExtensions).
func (l *cliResourceLoader) GetExtensions() coding.LoadExtensionsResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.extensions
}

// Reload is ReloadWith under a background context.
func (l *cliResourceLoader) Reload(options ...coding.ResourceLoaderReloadOptions) error {
	return l.ReloadWith(context.Background(), options...)
}

// ReloadWith rereads the build's resources and reloads its extensions under ctx (resource-loader.ts reload). An error leaves the loader's resources as they were.
func (l *cliResourceLoader) ReloadWith(ctx context.Context, _ ...coding.ResourceLoaderReloadOptions) error {
	if l.reload == nil {
		return nil
	}
	reloaded, err := l.reload(ctx)
	if err != nil {
		return err
	}
	l.set(reloaded.prompts, reloaded.skills, reloaded.sourceInfo)
	l.setPrompt(reloaded.prompt, reloaded.agents)
	l.mu.Lock()
	l.extensions = reloaded.extensions
	l.mu.Unlock()
	return nil
}

func (l *cliResourceLoader) catalog() codingagent.SlashCommandCatalog {
	return codingagent.SlashCommandCatalog{CWD: l.cwd, AgentDir: l.agentDir, SourceInfo: l.sourceInfo}
}

func (l *cliResourceLoader) GetPrompts() coding.PromptsResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	return coding.PromptsResult{Prompts: slices.Clone(l.prompts)}
}

func (l *cliResourceLoader) GetSkills() coding.SkillsResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	return coding.SkillsResult{Skills: slices.Clone(l.skills)}
}

// ExtendResources adds the prompt templates and skills extensions discovered, recording each path's extension provenance (resource-loader.ts extendResources). A discovered template whose name is already loaded is skipped; skills are deduplicated by name. Theme paths only record their provenance.
func (l *cliResourceLoader) ExtendResources(paths coding.ResourceExtensionPaths) error {
	normalize := func(entries []coding.ExtensionResourcePath) ([]coding.ExtensionResourcePath, error) {
		out := make([]coding.ExtensionResourcePath, 0, len(entries))
		for _, entry := range entries {
			path, err := codingagent.ResolvePath(widthx.JSTrim(entry.Path), l.cwd)
			if err != nil {
				return nil, err
			}
			out = append(out, coding.ExtensionResourcePath{Path: path, Metadata: entry.Metadata})
		}
		return out, nil
	}
	skillPaths, err := normalize(paths.SkillPaths)
	if err != nil {
		return err
	}
	promptPaths, err := normalize(paths.PromptPaths)
	if err != nil {
		return err
	}
	themePaths, err := normalize(paths.ThemePaths)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	sourceInfo := make(map[string]codingagent.ResourceSourceInfo, len(l.sourceInfo)+len(skillPaths)+len(promptPaths)+len(themePaths))
	maps.Copy(sourceInfo, l.sourceInfo)
	record := func(entries []coding.ExtensionResourcePath, kind string) []string {
		out := make([]string, 0, len(entries))
		for _, entry := range entries {
			metadata := entry.Metadata
			sourceInfo[entry.Path] = codingagent.ResourceSourceInfo{
				Path: entry.Path, ResourceType: kind, Enabled: true,
				Scope: metadata.Scope, Origin: metadata.Origin, Source: metadata.Source, BaseDir: metadata.BaseDir,
			}
			out = append(out, entry.Path)
		}
		return out
	}
	skillFiles := record(skillPaths, "skills")
	promptFiles := record(promptPaths, "prompts")
	record(themePaths, "themes")
	l.sourceInfo = sourceInfo
	catalog := l.catalog()

	if len(promptFiles) > 0 {
		prompts := slices.Clone(l.prompts)
		seen := make(map[string]struct{}, len(prompts))
		for _, template := range prompts {
			seen[template.Name] = struct{}{}
		}
		for _, template := range codingagent.LoadPromptTemplates("", "", promptFiles...).Templates {
			if _, duplicate := seen[template.Name]; duplicate {
				continue
			}
			seen[template.Name] = struct{}{}
			prompts = append(prompts, template)
		}
		l.prompts = catalog.WithPromptSources(prompts)
	}
	if len(skillFiles) == 0 {
		return nil
	}
	skills := slices.Clone(l.skills)
	for _, path := range skillFiles {
		// upstream: packages/coding-agent/src/core/resource-loader.ts:extendResources keeps a path's load failure and each skill's diagnostics in getSkills().diagnostics, which only interactive mode displays; print, JSON and RPC mode report nothing.
		loaded, _ := codingagent.LoadSkillsFromPath(path)
		for _, skill := range loaded {
			if jsstring.Trim(skill.Description) != "" {
				skills = append(skills, skill)
			}
		}
	}
	l.skills = catalog.WithSkillSources(codingagent.DeduplicateSkills(skills))
	return nil
}

var _ coding.ResourceLoader = (*cliResourceLoader)(nil)
