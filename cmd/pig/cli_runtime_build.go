package main

// Ports packages/coding-agent/src/main.ts createRuntime: the cwd-bound half of the runtime factory.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/tui"
)

// cliStartupFailure is a failed build. Startup reports it in the form the failing step used and exits. A replacement receives it as an ordinary error.
type cliStartupFailure struct {
	message string
	report  func(message string)
}

func (f *cliStartupFailure) Error() string { return f.message }

func cliCLIError(format string, args ...any) *cliStartupFailure {
	return &cliStartupFailure{message: fmt.Sprintf(format, args...), report: func(message string) { printCLIError("%s", message) }}
}

func cliPigError(format string, args ...any) *cliStartupFailure {
	return &cliStartupFailure{message: fmt.Sprintf(format, args...), report: func(message string) { fmt.Fprintf(os.Stderr, "pig: %s\n", message) }}
}

// cliRuntimeBuilder holds the process-fixed inputs of Pi's createRuntime closure and builds the cwd-bound state of one Session runtime: services, resources, extension host, model, tools and system prompt.
type cliRuntimeBuilder struct {
	mode                appMode
	flags               CLIFlags
	agentDir            string
	launchCWD           string
	activePiglet        *piglet.Piglet
	activePigletBaked   bool
	settingsManager     *codingagent.SettingsManager
	settingsDiagnostics []codingagent.AgentSessionRuntimeDiagnostic
	startupUIOptions    codingagent.StartupUIOptions
	trustStore          *codingagent.ProjectTrustStore
	trustByCWD          map[string]bool
	stageExtensionSDKs  func()
}

// cliBuildInput selects one build.
type cliBuildInput struct {
	CWD string
	// Manager is the Session log the runtime will wrap. Model selection restores from it.
	Manager *coding.SessionManager
	// Continuing reports a resumed, continued or forked Session at startup.
	Continuing bool
	// Startup is true for the process's first build. Replacements never prompt for trust and reuse no preloaded host.
	Startup bool
	// StartupExtensions owns the pre-trust host that the final extension load reuses.
	StartupExtensions *startupExtensionSet
	// ProjectTrustUI is the live interactive UI a replacement's trust prompt uses (Pi's projectTrustContext, main.ts:751). Without it a replacement never prompts.
	ProjectTrustUI extension.UIContext
}

// cliBuild is everything one Session runtime owns besides the Session itself.
type cliBuild struct {
	CWD            string
	Flags          CLIFlags
	ResourceFlags  CLIFlags
	ProjectTrusted bool
	Services       *coding.Services
	Settings       codingagent.Settings
	Llama          *llama.Host

	StartupDiagnostics           []codingagent.AgentSessionRuntimeDiagnostic
	PreTrustExtensionDiagnostics []codingagent.AgentSessionRuntimeDiagnostic
	ExtensionDiagnostics         []codingagent.AgentSessionRuntimeDiagnostic

	PromptPaths     []string
	ThemePaths      []string
	SkillInputs     []string
	ExtensionScopes *[]string
	SkillScopes     *[]string
	SourceResolver  *startupExtensionSourceResolver

	ExtraExtConfigs         []subprocess.ExtConfig
	EmbeddedCells           []subprocess.EmbeddedCell
	ReloadExtensionConfigs  func() []subprocess.ExtConfig
	SubprocessExtensions    []extension.Extension
	Host                    *subprocess.Host
	Bridge                  *subprocess.UIBridge
	ReloadBuiltinExtensions func() []extension.Extension
	BuiltinExtensions       []extension.Extension
	Extensions              []extension.Extension

	Model          *ai.Model
	NoModelWarning string
	// Selected is the model selection with its warnings and errors. ModelErr is its failure: startup reports it after the metadata commands, a replacement fails on it.
	Selected startupModel
	ModelErr error
	// RPCSourceInfo is the resource provenance RPC stamps on extension configs and commands.
	RPCSourceInfo map[string]codingagent.ResourceSourceInfo
	SkillCatalog  codingagent.SlashCommandCatalog
	SkillDefs     []*codingagent.SkillDef

	SkillLoad            *skillLoadResult
	AgentToolNames       []string
	Allowed              map[string]struct{}
	ActiveBuiltin        map[string]struct{}
	ExcludedTools        map[string]struct{}
	SkipBuiltinTools     bool
	ContextFiles         []codingagent.ContextFile
	ResolvedPrompts      resolvedPromptInputs
	PromptOptions        prompts.Options
	SystemPrompt         string
	SystemPromptSections ai.OrderedSections
	SystemPromptOptions  extension.BuildSystemPromptOptions
	BeforeToolCall       []agent.BeforeToolCallHook
}

func (b *cliBuild) skills() []*codingagent.SkillDef { return b.SkillDefs }

// buildResources runs Pi's createAgentSessionServices for the destination cwd: project trust, services, resource discovery and the extension load.
func (b *cliRuntimeBuilder) buildResources(ctx context.Context, in cliBuildInput) (*cliBuild, error) {
	flags := b.flags
	cwd := in.CWD
	resourceFlags, err := resolveCLIResourceFlags(flags, b.launchCWD)
	if err != nil {
		return nil, cliCLIError("%v", err)
	}
	build := &cliBuild{CWD: cwd, ResourceFlags: resourceFlags}
	hasTrustResources := codingagent.HasTrustRequiringProjectResources(cwd)
	projectTrusted := flags.ProjectTrustOverride != nil && *flags.ProjectTrustOverride
	if flags.ProjectTrustOverride == nil && !hasTrustResources {
		projectTrusted = true
	}
	cached, hasCached := b.trustByCWD[cwd]
	if flags.ProjectTrustOverride == nil && hasTrustResources && hasCached {
		projectTrusted = cached
	}
	resolveTrust := flags.ProjectTrustOverride == nil && hasTrustResources && !hasCached

	traceSDKLockWaits()
	trace.Mark("pre-services")
	services, err := coding.NewServices(coding.ServicesOptions{
		CWD:            cwd,
		AgentDir:       b.agentDir,
		ProjectTrusted: new(projectTrusted),
	})
	if err != nil {
		return nil, cliPigError("services init: %v", err)
	}
	build.Services = services
	trace.Mark("services-created")
	build.Llama = startBuiltInLlama(ctx, services)
	// --use-theme and --tui-mode reach only InteractiveMode (InitialThemeSetting
	// and TuiMode), so the runtime settings keep the saved values as Pi's do.
	settings := services.Settings()

	if in.Startup {
		trace.Mark("sdk-sync-start")
		b.stageExtensionSDKs()
		trace.Mark("sdk-sync-done")
	}

	startupExtensions := in.StartupExtensions
	if startupExtensions == nil {
		startupExtensions = &startupExtensionSet{}
	}
	build.SourceResolver = newStartupExtensionSourceResolver(nil)
	resolver := build.SourceResolver
	failed := true
	defer func() {
		if failed {
			if in.StartupExtensions == nil {
				startupExtensions.close()
			}
			services.Close()
		}
	}()

	if resolveTrust {
		// resource-loader.ts:549 resolves settings before loading pre-trust extension factories.
		if err := validateConfiguredResourceEntries(cwd, b.agentDir, services.SettingsManager(), false); err != nil {
			return nil, cliCLIError("%v", err)
		}
		trace.Mark("trust-preload-start")
		userScope := []string{"user"}
		preTrustConfigs := collectExtensionConfigs(cwd, b.agentDir, services.SettingsManager(), resourceFlags, &userScope, resolver.Resolve)
		preTrustExts, _, preTrustBridge, preTrustLoadErrs := loadFinalSubprocessExtensions(
			ctx,
			cwd,
			b.mode.extensionMode(),
			services.Registry().ModelRegistry,
			preTrustConfigs,
			nil,
			nil,
			startupExtensions,
		)
		trace.Mark("trust-preload-done")
		build.PreTrustExtensionDiagnostics = extensionLoadDiagnostics(preTrustLoadErrs)
		var trustRunner *inproc.Runner
		if len(preTrustExts) > 0 {
			trustRunner = inproc.NewRunner(preTrustExts, cwd)
		}
		trustUI := extension.NoopUIContext
		interactiveTrust := in.Startup && b.mode == appModeInteractive && !flags.Help && flags.ListModels == "" && !flags.ListModelsAll
		if interactiveTrust {
			trustUI = newStartupTrustUI(b.startupUIOptions)
		} else if in.ProjectTrustUI != nil {
			trustUI = in.ProjectTrustUI
		}
		if trustUI != extension.NoopUIContext && preTrustBridge != nil {
			preTrustBridge.SetUIContext(trustUI)
		}
		globalDefaultTrust := b.settingsManager.GetGlobalSettings().DefaultProjectTrust
		if globalDefaultTrust == "" {
			globalDefaultTrust = "ask"
		}
		projectTrusted, err = resolveProjectTrusted(ctx, projectTrustResolutionOptions{
			CWD:     cwd,
			Store:   b.trustStore,
			Default: globalDefaultTrust,
			Runner:  trustRunner,
			UI:      trustUI,
			OnExtensionError: func(message string) {
				fmt.Fprintln(os.Stderr, "warning:", message)
			},
		})
		if err != nil {
			return nil, cliCLIError("resolve project trust: %v", err)
		}
		b.trustByCWD[cwd] = projectTrusted
		services.SettingsManager().SetProjectTrusted(projectTrusted)
		settings = services.Settings()
		trace.Mark("trust-resolved")
	}
	build.ProjectTrusted = projectTrusted
	if in.Startup {
		tui.SetCapabilityOverrides(services.SettingsManager().GetTerminalCapabilityOverrides())
	}
	build.StartupDiagnostics = codingagent.DeduplicateDiagnostics(append(slices.Clone(b.settingsDiagnostics), codingagent.CollectSettingsDiagnostics(services.SettingsManager())...))

	if len(flags.Models) > 0 {
		settings.EnabledModels = flags.Models
	}

	trace.Mark("packages-reinstall-start")
	// package-manager.ts:912-926 dedupes package identities before installing any missing package.
	if err := validateConfiguredPackageSources(services.SettingsManager(), projectTrusted); err != nil {
		return nil, cliCLIError("%v", err)
	}
	if reinstalled, missingPkgs := packagemanager.EnsureConfiguredPackagesInstalled(cwd, services.SettingsManager().AgentDir(), services.SettingsManager()); len(reinstalled)+len(missingPkgs) > 0 {
		if len(reinstalled) > 0 {
			fmt.Fprintf(os.Stderr, "[pig] Reinstalled missing packages: %s\n", strings.Join(reinstalled, ", "))
		}
		if len(missingPkgs) > 0 {
			fmt.Fprintf(os.Stderr, "[pig] Could not install: %s (run `pig update` when online)\n", strings.Join(missingPkgs, ", "))
		}
	}
	trace.Mark("packages-reinstall-done")

	if err := validateConfiguredResourceEntries(cwd, b.agentDir, services.SettingsManager(), projectTrusted); err != nil {
		return nil, cliCLIError("%v", err)
	}
	build.PromptPaths = collectPromptPaths(cwd, b.agentDir, services.SettingsManager(), resourceFlags, projectTrusted, resolver.Resolve)
	build.ThemePaths = collectThemePaths(cwd, b.agentDir, services.SettingsManager(), resourceFlags, projectTrusted, resolver.Resolve)
	extensionScopes := pigletAmbientSources(b.activePiglet, "extensions")
	skillScopes := pigletAmbientSources(b.activePiglet, "skills")
	if !projectTrusted {
		extensionScopes = trustedAmbientScopes(extensionScopes)
		skillScopes = trustedAmbientScopes(skillScopes)
	}
	if b.activePigletBaked {
		empty := []string{}
		extensionScopes = &empty
		skillScopes = &empty
	}
	build.ExtensionScopes, build.SkillScopes = extensionScopes, skillScopes
	if err := validateConfiguredPackagesForStartup(cwd, services.SettingsManager(), func(scope string) bool {
		return !resourceFlags.NoExtensions && packagemanager.PackageScopeEnabled(extensionScopes, scope)
	}, resolver.Resolve); err != nil {
		build.StartupDiagnostics = append(build.StartupDiagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "warning", Message: err.Error()})
	}
	trace.Mark("packages-validated")
	build.SkillInputs = collectSkillInputs(cwd, b.agentDir, services.SettingsManager(), resourceFlags, skillScopes, resolver.Resolve)
	trace.Mark("extension-discovery-start")
	extraExtConfigs := collectExtensionConfigs(cwd, b.agentDir, services.SettingsManager(), resourceFlags, extensionScopes, resolver.Resolve)
	trace.Mark("extension-discovery-done")

	applyPigletPreStart(b.activePiglet, &flags)

	if in.Startup {
		trace.Mark("services-init")
	}
	registry := services.Registry()

	if b.activePiglet != nil {
		pigletExtConfigs := resolvePigletExtConfigs(b.activePiglet)
		if b.activePigletBaked && len(b.activePiglet.Extensions) > 0 {
			flags.NoExtensions = true
			extraExtConfigs = mergeExtConfigs(append(fusedConfigsForPiglet(b.activePiglet), extraExtConfigs...))
		} else if len(pigletExtConfigs) > 0 {
			flags.NoExtensions = true
			extraExtConfigs = mergeExtConfigs(append(pigletExtConfigs, extraExtConfigs...))
		}
	}
	build.ExtraExtConfigs = extraExtConfigs

	var embeddedCells []subprocess.EmbeddedCell
	if b.activePigletBaked {
		embeddedCells = embeddedCellsFromCellpack(cellpack.LoadedCells())
	}
	build.EmbeddedCells = embeddedCells
	activePiglet := b.activePiglet
	build.ReloadExtensionConfigs = func() []subprocess.ExtConfig {
		configs := collectExtensionConfigs(cwd, b.agentDir, services.SettingsManager(), resourceFlags, extensionScopes)
		if activePiglet != nil {
			configs = append(resolvePigletExtConfigs(activePiglet), configs...)
		}
		return mergeExtConfigs(configs)
	}
	// Every mode, RPC included, loads its final extension set here once, as upstream createAgentSessionServices does before model resolution.
	finalExtConfigs, reloadFinalExtConfigs := extraExtConfigs, build.ReloadExtensionConfigs
	if b.mode == appModeRPC {
		// get_commands reports the provenance of extension commands, so each config carries its resolved source before the load.
		build.RPCSourceInfo = resourceSourceInfoProvider(cwd, b.agentDir, services.SettingsManager(), resourceFlags, resolver.Resolve)()
		finalExtConfigs, reloadFinalExtConfigs = rpcExtensionConfigs(extraExtConfigs, cwd, b.agentDir, build.RPCSourceInfo), nil
	}
	var extensionLoadErrs []error
	// pig divergence (D70): every build, including a session replacement's, starts its own extension host and processes.
	// Pi queues the provider registrations of the extensions it loads and flushes them only after every factory has finished, immediately before an awaited local refresh (agent-session-services.ts:158-182), so no Provider callback runs while extensions load. The hold keeps a registration from starting that refresh early, including for a replacement Session's build (D70).
	releaseRegistrationRefresh := registry.HoldRegistrationRefresh()
	defer releaseRegistrationRefresh()
	if !flags.NoExtensions || len(finalExtConfigs) > 0 || len(embeddedCells) > 0 {
		build.SubprocessExtensions, build.Host, build.Bridge, extensionLoadErrs = loadFinalSubprocessExtensions(ctx, cwd, b.mode.extensionMode(), registry.ModelRegistry, finalExtConfigs, embeddedCells, reloadFinalExtConfigs, startupExtensions)
	}
	// Pi createAgentSessionServices awaits a local refresh after it flushes extension provider registrations, before model resolution and --list-models (agent-session-services.ts:158-182). The registrations only queued their refresh, and this call yields to it (model-runtime.ts:744-750). Later host registrations start their own refresh.
	if in.Startup {
		startupRegistrationRefreshReady.Store(true)
	}
	services.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if b.mode == appModeInteractive {
		// Upstream /reload rediscovers extensions even when none loaded at
		// startup, so interactive mode keeps a reload-capable host.
		build.Host, build.Bridge = ensureReloadableExtensionHost(build.Host, build.Bridge, flags.NoExtensions, cwd, registry.ModelRegistry, build.ReloadExtensionConfigs)
	}
	if in.StartupExtensions == nil {
		// The final load adopted the pre-trust host, if any. This build owns it now.
		startupExtensions.host = nil
		startupExtensions.bridge = nil
	}
	ownsHost := func() {
		if build.Host != nil {
			build.Host.Shutdown("extension load failure")
		}
	}
	defer func() {
		if failed {
			ownsHost()
		}
	}()

	if activePiglet != nil {
		build.ReloadBuiltinExtensions = func() []extension.Extension {
			return []extension.Extension{piglet.BuildExtensionWithPiglet(activePiglet)}
		}
		build.BuiltinExtensions = build.ReloadBuiltinExtensions()
	}
	build.Extensions = codingagent.ExtensionsInLoadOrder(build.SubprocessExtensions, build.BuiltinExtensions)
	build.ExtensionDiagnostics = slices.Concat(build.PreTrustExtensionDiagnostics, extensionLoadDiagnostics(extensionLoadErrs), extensionConflictDiagnostics(codingagent.DetectExtensionConflicts(build.Extensions)))
	trace.Mark("extensions-loaded")

	build.Flags = flags
	build.Settings = settings
	failed = false
	return build, nil
}

// buildSession resolves the model, skills, tools and system prompt against the build's resources, as Pi's createRuntime does before createAgentSessionFromServices.
func (b *cliRuntimeBuilder) buildSession(ctx context.Context, build *cliBuild, in cliBuildInput) error {
	flags := build.Flags
	cwd := build.CWD
	services := build.Services
	settings := build.Settings
	trace.Mark("pre-model")
	modelFlag := flags.Model
	if modelFlag == "" {
		modelFlag = pigletModelSpec(b.activePiglet)
	}
	selected, modelErr := selectStartupModel(ctx, startupModelOptions{
		SessionManager: in.Manager,
		CLIProvider:    flags.Provider,
		CLIModel:       modelFlag,
		CLIThinking:    flags.Thinking,
		ScopePatterns:  settings.EnabledModels,
		Continuing:     in.Continuing,
		APIKey:         flags.APIKey,
	}, settings, services)
	build.Selected, build.ModelErr = selected, modelErr
	build.Model = selected.Model
	build.NoModelWarning = selected.ModelFallbackMessage
	if build.Model == nil && build.NoModelWarning == "" {
		build.NoModelWarning = codingagent.FormatNoModelsAvailableMessage()
	}
	if selected.Thinking != "" && flags.Thinking == "" {
		flags.Thinking = selected.Thinking
	}
	if flags.Thinking == "" && b.activePiglet != nil && b.activePiglet.Model != nil && b.activePiglet.Model.Thinking != "" {
		flags.Thinking = b.activePiglet.Model.Thinking
	}
	trace.Mark("model-resolved")

	slr, err := resolveAndLoadSkills(b.activePiglet, build.SkillInputs)
	if err != nil {
		return cliCLIError("%v", err)
	}
	build.SkillLoad = slr
	build.SkillInputs = slr.Paths
	build.SkillCatalog = codingagent.SlashCommandCatalog{CWD: cwd, AgentDir: b.agentDir, SourceInfo: resourceSourceInfoProvider(cwd, b.agentDir, services.SettingsManager(), build.ResourceFlags, build.SourceResolver.Resolve)()}
	skillDefs := build.SkillCatalog.WithSkillSources(slr.Defs)
	build.SkillDefs = skillDefs

	registryToolNames := tools.BuiltinToolNames()
	agentToolNames := []string{"read", "bash", "edit", "write"}
	if settings.DefaultTools != nil {
		agentToolNames = slices.Clone(settings.DefaultTools)
	}
	toolHints := prompts.DefaultToolSnippets()
	toolGuidelines := tools.DefaultToolGuidelines()
	allowed := map[string]struct{}(nil)
	var activeBuiltin map[string]struct{}
	skipBuiltinTools := flags.NoBuiltinTools
	if flags.NoBuiltinTools {
		agentToolNames = []string{}
	}
	// An explicit --tools list outranks --no-tools and --no-builtin-tools, as sdk.ts allowedToolNames = tools ?? (noTools === "all" ? [] : undefined) and initialActiveToolNames = tools ?? (noTools ? [] : defaults).
	switch {
	case flags.Tools != nil:
		allowed = make(map[string]struct{}, len(flags.Tools))
		for _, t := range flags.Tools {
			allowed[t] = struct{}{}
		}
		filtered := make([]string, 0, len(flags.Tools))
		for _, n := range registryToolNames {
			if _, ok := allowed[n]; ok {
				filtered = append(filtered, n)
			}
		}
		agentToolNames = filtered
		skipBuiltinTools = false
	case flags.NoTools:
		allowed = make(map[string]struct{})
		agentToolNames = []string{}
		skipBuiltinTools = false
	default:
		if !flags.NoBuiltinTools {
			activeBuiltin = make(map[string]struct{}, len(agentToolNames))
			for _, t := range agentToolNames {
				activeBuiltin[t] = struct{}{}
			}
		}
	}
	var excludedTools map[string]struct{}
	if len(flags.ExcludeTools) > 0 {
		excludedTools = make(map[string]struct{}, len(flags.ExcludeTools))
		for _, t := range flags.ExcludeTools {
			excludedTools[t] = struct{}{}
		}
		filtered := agentToolNames[:0]
		for _, n := range agentToolNames {
			if _, ok := excludedTools[n]; !ok {
				filtered = append(filtered, n)
			}
		}
		agentToolNames = filtered
	}
	promptSkills := make([]prompts.Skill, 0, len(skillDefs))
	for _, s := range skillDefs {
		promptSkills = append(promptSkills, prompts.Skill{Name: s.Name, Description: s.Description, Path: s.Path, DisableModelInvocation: s.DisableModelInvocation})
	}
	trace.Mark("pre-system-prompt")
	projectCtxFiles := loadContextFiles(cwd, b.agentDir, flags.NoContextFiles)
	promptCtxFiles := toPromptContextFiles(projectCtxFiles)
	resolvedPrompts := resolvePromptInputs(cwd, b.agentDir, flags, build.ProjectTrusted)
	promptOptions := prompts.Options{
		Cwd:            cwd,
		Tools:          agentToolNames,
		ToolHints:      toolHints,
		ToolGuidelines: toolGuidelines,
		Skills:         promptSkills,
		PigDocsPath:    filepath.Join(codingagent.ConfigRoot(), "docs"),
		AppendMode:     "append",
		ContextFiles:   promptCtxFiles,
	}
	if resolvedPrompts.custom != "" {
		promptOptions.CustomPrompt = resolvedPrompts.custom
		promptOptions.AppendMode = "replace"
	}
	promptOptions.AppendSystemPrompt = resolvedPrompts.append

	build.SystemPromptSections = prompts.BuildSystemPromptSections(promptOptions)
	build.SystemPrompt = prompts.BuildDefaultPrompt(promptOptions)

	extContextFiles := make([]extension.SystemPromptContextFile, 0, len(promptCtxFiles))
	for _, cf := range promptCtxFiles {
		extContextFiles = append(extContextFiles, extension.SystemPromptContextFile{Path: cf.Path, Content: cf.Content})
	}
	extSkills := extensionPromptSkills(skillDefs)
	build.SystemPromptOptions = extension.BuildSystemPromptOptions{
		CustomPrompt:       resolvedPrompts.custom,
		CustomPromptSet:    resolvedPrompts.customSet,
		SelectedTools:      append([]string{}, agentToolNames...),
		ToolSnippets:       toolHints,
		ToolGuidelines:     toolGuidelines,
		AppendSystemPrompt: resolvedPrompts.append,
		Cwd:                cwd,
		ContextFiles:       extContextFiles,
		Skills:             extSkills,
	}
	build.Flags = flags
	build.AgentToolNames = agentToolNames
	build.Allowed = allowed
	build.ActiveBuiltin = activeBuiltin
	build.ExcludedTools = excludedTools
	build.SkipBuiltinTools = skipBuiltinTools
	build.ContextFiles = projectCtxFiles
	build.ResolvedPrompts = resolvedPrompts
	build.PromptOptions = promptOptions
	return nil
}

// reportCLIStartupFailure reports a failed startup build in the form its failing step used.
func reportCLIStartupFailure(err error) {
	if failure, ok := errors.AsType[*cliStartupFailure](err); ok {
		failure.report(failure.message)
		return
	}
	printCLIError("%v", err)
}
