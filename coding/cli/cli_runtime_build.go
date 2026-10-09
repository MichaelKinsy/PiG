package cli

// Ports packages/coding-agent/src/main.ts createRuntime: the cwd-bound half of the runtime factory.

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	flags               Args
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
	// extensionFactories are MainOptions.ExtensionFactories, the compiled-in extensions the program's main adds after the built-in ones (main.ts:575).
	extensionFactories []extension.InlineExtension
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
	// ExtensionFlagValues are the validated values extensions read with getFlag.
	ExtensionFlagValues map[string]any
	CWD                 string
	Flags               Args
	ResourceFlags       Args
	ProjectTrusted      bool
	Services            *coding.AgentSessionServices
	Settings            codingagent.Settings

	// SDKDrift are the extensions that failed to build only because they were written for an older SDK.
	SDKDrift []extensionDrift

	StartupDiagnostics           []codingagent.AgentSessionRuntimeDiagnostic
	PreTrustExtensionDiagnostics []codingagent.AgentSessionRuntimeDiagnostic
	ExtensionDiagnostics         []codingagent.AgentSessionRuntimeDiagnostic

	PromptPaths     []string
	ThemePaths      []string
	SkillInputs     []string
	ExtensionScopes *[]string
	SkillScopes     *[]string
	SourceResolver  *startupExtensionSourceResolver

	ExtraExtConfigs        []subprocess.ExtConfig
	EmbeddedCells          []subprocess.EmbeddedCell
	ReloadExtensionConfigs func() []subprocess.ExtConfig
	SubprocessExtensions   []extension.Extension
	Host                   *subprocess.Host
	// Runtime is the extension runtime the compiled-in factories registered against; the Session's runner binds it. A build with a Host shares the Host's.
	Runtime                 *extension.ExtensionRuntime
	Bridge                  *subprocess.UIBridge
	ReloadBuiltinExtensions func() []extension.Extension
	BuiltinExtensions       []extension.Extension
	Extensions              []extension.Extension

	Model                *ai.Model
	ModelFallbackMessage string
	// Selected is the model selection with its warnings and errors. ModelErr is its failure: startup reports it after the metadata commands, a replacement fails on it.
	Selected startupModel
	ModelErr error
	// RPCSourceInfo is the resource provenance RPC stamps on extension configs and commands.
	RPCSourceInfo map[string]codingagent.ResourceSourceInfo
	SkillCatalog  codingagent.SlashCommandCatalog
	SkillDefs     []*codingagent.SkillDef

	SkillLoad      *skillLoadResult
	AgentToolNames []string
	Allowed        map[string]struct{}
	ActiveBuiltin  map[string]struct{}
	// InitialActiveToolNames are the Session's initial active tools in order (sdk.ts:274-276).
	InitialActiveToolNames []string
	ExcludedTools          map[string]struct{}
	SkipBuiltinTools       bool
	// DefaultToolModifiers are the --tools +name/-name entries (sdk.ts:285).
	DefaultToolModifiers []string
	// NoTools is "builtin" for --no-builtin-tools with +name/-name entries: the entries name built-in tools, which stay registered, and the Session does not treat the selection as the defaultTools setting (sdk.ts:472 usesDefaultTools).
	NoTools              string
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
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{
		CWD:            cwd,
		AgentDir:       b.agentDir,
		ProjectTrusted: new(projectTrusted),
		// main.ts:753-754: the model runtime lives as long as the run; extension flags are checked once the extensions load.
		ModelRuntimeSignal:  ctx,
		ExtensionFlagValues: flags.extensionFlagValues(),
	})
	if err != nil {
		return nil, cliPigError("services init: %v", err)
	}
	build.Services = services
	trace.Mark("services-created")
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

	// preTrustInline are the inline extensions the pre-trust pass loaded; inlineBus is the event bus every compiled-in factory shares.
	var preTrustInline []extension.Extension
	var inlineErrs []extensionSetError
	inlineBus := extension.CreateEventBus()
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
		// resource-loader.ts loadProjectTrustExtensions: the inline extensions load in the pre-trust pass, so their project_trust handlers take part, and the final pass keeps them (loadFinalExtensionSet).
		if hasInlineExtensions(b.extensionFactories) {
			if startupExtensions.host == nil {
				// The inline extensions register against the runtime of the host the final pass adopts.
				startupExtensions.host, startupExtensions.bridge = newSubprocessExtensionHost(cwd, b.mode.extensionMode(), services.Registry().ModelRegistry)
			}
			preTrustInline, inlineErrs = (&extensionSetLoader{CWD: cwd, AgentDir: b.agentDir, Settings: services.SettingsManager(), Flags: resourceFlags, Inline: b.extensionFactories, Runtime: startupExtensions.host.Runtime(), EventBus: inlineBus}).loadInlineExtensions()
			// Non-nil even when every factory failed: the final pass runs no factory again (resource-loader.ts:772-773).
			if preTrustInline == nil {
				preTrustInline = []extension.Extension{}
			}
			preTrustExts = append(preTrustExts, preTrustInline...)
		}
		build.SDKDrift = append(build.SDKDrift, sdkDriftOf(preTrustLoadErrs, preTrustConfigs)...)
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
		trustMode, trustHasUI, trustLiveUI := projectTrustPromptMode(b.mode, flags.Help || flags.ListModels != "" || flags.ListModelsAll, interactiveTrust, in.ProjectTrustUI != nil)
		projectTrusted, err = resolveProjectTrusted(ctx, projectTrustResolutionOptions{
			CWD:     cwd,
			Store:   b.trustStore,
			Default: globalDefaultTrust,
			Runner:  trustRunner,
			UI:      trustUI,
			Mode:    trustMode,
			HasUI:   trustHasUI,
			LiveUI:  trustLiveUI,
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

	// upstream: main.ts:810 `parsed.models ?? settingsManager.getEnabledModels()`: an explicit empty --models list replaces the setting.
	if flags.Models != nil {
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
	// A package manifest that lists host-provided packages under `dependencies` warns; one that cannot be parsed fails the load (resource-loader.ts:66-93,576-577).
	packageWarnings, err := extensionPackageWarnings(extraExtConfigs)
	if err != nil {
		return nil, cliCLIError("%v", err)
	}

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
	if build.Host == nil && preTrustInline != nil {
		// The pre-trust inline extensions registered against this host's runtime; the build keeps it when no file extension load adopted it.
		build.Host, build.Bridge = startupExtensions.host, startupExtensions.bridge
	}
	for _, drift := range sdkDriftOf(extensionLoadErrs, finalExtConfigs) {
		if !slices.ContainsFunc(build.SDKDrift, func(known extensionDrift) bool { return known.Source == drift.Source }) {
			build.SDKDrift = append(build.SDKDrift, drift)
		}
	}
	virtualModelDiagnostics := flushExtensionHostVirtualModels(build.Host, registry)
	// Pi createAgentSessionServices awaits a local refresh after it flushes extension provider registrations, before model resolution and --list-models (agent-session-services.ts:158-182). The registrations only queued their refresh, and this call yields to it (model-runtime.ts:744-750). Later host registrations start their own refresh.
	if in.Startup {
		startupRegistrationRefreshReady.Store(true)
	}
	services.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if b.mode == appModeInteractive {
		// Upstream /reload rediscovers extensions even when none loaded at
		// startup, so interactive mode keeps a reload-capable host.
		build.Host, build.Bridge = ensureReloadableExtensionHost(build.Host, build.Bridge, flags.NoExtensions, cwd, registry.ModelRegistry, build.ReloadExtensionConfigs)
		// A host created for /reload has loaded nothing; binding it applies what a reload registers.
		virtualModelDiagnostics = append(virtualModelDiagnostics, flushExtensionHostVirtualModels(build.Host, registry)...)
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

	// The built-in extensions the settings and flags enable load after the file extensions (resource-loader.ts:703-735).
	builtinLoader := &extensionSetLoader{CWD: cwd, AgentDir: b.agentDir, Settings: services.SettingsManager(), Flags: resourceFlags, Inline: slices.Concat(nativeBuiltInExtensions(services.SettingsManager()), b.extensionFactories), EventBus: inlineBus}
	if build.Host != nil {
		builtinLoader.Runtime = build.Host.Runtime()
	}
	build.Runtime = builtinLoader.FactoryRuntime()
	// loadBuiltins loads the built-in extensions the settings and flags enable, then the inline extensions (inline, when the pre-trust pass loaded them), in loadFinalExtensionSet order, and leaves out the replaceable ones a later or earlier extension replaces (resource-loader.ts:703-789).
	loadBuiltins := func(inline []extension.Extension) ([]extension.Extension, []extensionSetError, []extensionSetWarning) {
		builtins, errs, warnings := builtinLoader.loadBuiltinsAfter(build.SubprocessExtensions)
		if inline == nil {
			var loadErrs []extensionSetError
			inline, loadErrs = builtinLoader.loadInlineExtensions()
			errs = append(errs, loadErrs...)
		}
		if len(inline) == 0 {
			return builtins, errs, warnings
		}
		var inlineWarnings []extensionSetWarning
		kept := omitReplacedExtensions(slices.Concat(build.SubprocessExtensions, builtins, inline), &inlineWarnings)
		inProcess := slices.DeleteFunc(kept, func(ext extension.Extension) bool {
			return slices.ContainsFunc(build.SubprocessExtensions, func(file extension.Extension) bool { return file.Path == ext.Path })
		})
		return inProcess, errs, mergeExtensionWarnings(warnings, inlineWarnings)
	}
	builtinExtensions, builtinErrs, replacementWarnings := loadBuiltins(preTrustInline)
	builtinErrs = append(inlineErrs, builtinErrs...)
	withPiglet := func(builtins []extension.Extension) []extension.Extension {
		if activePiglet == nil {
			return builtins
		}
		owner := pigletToolOwner(func() []extension.Extension { return build.Extensions })
		return append([]extension.Extension{piglet.BuildExtensionWithPigletTools(activePiglet, owner)}, builtins...)
	}
	// The program's own factories load even when every built-in is stripped (D92).
	if activePiglet != nil || len(builtInExtensions) > 0 || hasInlineExtensions(b.extensionFactories) {
		build.ReloadBuiltinExtensions = func() []extension.Extension {
			reloaded, _, _ := loadBuiltins(nil)
			return withPiglet(reloaded)
		}
		build.BuiltinExtensions = withPiglet(builtinExtensions)
	}
	build.Extensions = codingagent.ExtensionsInLoadOrder(build.SubprocessExtensions, build.BuiltinExtensions)
	// Pi lists the services' registration failures before the extension load errors (main.ts:789-800).
	// agent-session-services.ts:184 applies the flag values once the extensions are loaded; main.ts:789 lists those diagnostics before the load errors.
	var flagDiagnostics []codingagent.AgentSessionRuntimeDiagnostic
	build.ExtensionFlagValues, flagDiagnostics = services.ApplyExtensionFlagValues(build.Extensions)
	conflicts := codingagent.DetectExtensionConflicts(build.Extensions)
	build.ExtensionDiagnostics = slices.Concat(build.PreTrustExtensionDiagnostics, virtualModelDiagnostics, flagDiagnostics, extensionLoadDiagnostics(extensionLoadErrs), extensionErrorDiagnostics(builtinErrs), extensionConflictDiagnostics(conflicts), staleCopyDiagnostics(codingagent.DetectToolConflicts(build.Extensions), finalExtConfigs), extensionWarningDiagnostics(mergeExtensionWarnings(packageWarnings, replacementWarnings)))
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
	build.ModelFallbackMessage = selected.ModelFallbackMessage
	if build.Model == nil && build.ModelFallbackMessage == "" {
		build.ModelFallbackMessage = codingagent.FormatNoModelsAvailableMessage()
	}
	if selected.Thinking != "" && flags.Thinking == "" {
		flags.Thinking = selected.Thinking
	}
	if flags.Thinking == "" && b.activePiglet != nil && b.activePiglet.Model != nil && b.activePiglet.Model.Thinking != "" {
		flags.Thinking = b.activePiglet.Model.Thinking
	}
	trace.Mark("model-resolved")

	selection := resolveToolSelection(flags, settings.GetDefaultTools(), tools.BuiltinToolNames())
	agentToolNames, allowed, activeBuiltin, initialActive := selection.agentToolNames, selection.allowed, selection.activeBuiltin, selection.initialActive
	excludedTools, skipBuiltinTools := selection.excluded, selection.skipBuiltin
	build.Flags = flags
	build.AgentToolNames = agentToolNames
	build.Allowed = allowed
	build.ActiveBuiltin = activeBuiltin
	build.InitialActiveToolNames = initialActive
	build.ExcludedTools = excludedTools
	build.SkipBuiltinTools = skipBuiltinTools
	build.DefaultToolModifiers = selection.modifiers
	build.NoTools = selection.noTools
	return b.loadPromptResources(build, agentToolNames, resourceSourceInfoProvider(cwd, b.agentDir, services.SettingsManager(), build.ResourceFlags, build.SourceResolver.Resolve)())
}

// loadPromptResources discovers the build's skills and context files and derives the system prompt from them, as the resource loader and AgentSession._rebuildSystemPrompt do. sourceInfo is the provenance of the resolved resource entries. build.SkillInputs, build.Flags and build.ProjectTrusted select what is read; agentToolNames are the tools the prompt lists.
func (b *cliRuntimeBuilder) loadPromptResources(build *cliBuild, agentToolNames []string, sourceInfo map[string]codingagent.ResourceSourceInfo) error {
	cwd := build.CWD
	slr, err := resolveAndLoadSkills(b.activePiglet, build.SkillInputs)
	if err != nil {
		return cliCLIError("%v", err)
	}
	build.SkillLoad = slr
	build.SkillInputs = slr.Paths
	build.SkillCatalog = codingagent.SlashCommandCatalog{CWD: cwd, AgentDir: b.agentDir, SourceInfo: sourceInfo}
	skillDefs := build.SkillCatalog.WithSkillSources(slr.Defs)
	build.SkillDefs = skillDefs

	toolHints := prompts.DefaultToolSnippets()
	toolGuidelines := tools.DefaultToolGuidelines()
	promptSkills := make([]prompts.Skill, 0, len(skillDefs))
	for _, s := range skillDefs {
		promptSkills = append(promptSkills, prompts.Skill{Name: s.Name, Description: s.Description, Path: s.FilePath, DisableModelInvocation: s.DisableModelInvocation})
	}
	trace.Mark("pre-system-prompt")
	projectCtxFiles := loadContextFiles(cwd, b.agentDir, build.Flags.NoContextFiles)
	promptCtxFiles := toPromptContextFiles(projectCtxFiles)
	resolvedPrompts := resolvePromptInputs(cwd, b.agentDir, build.Flags, build.ProjectTrusted)
	promptOptions := prompts.Options{
		Cwd:            cwd,
		Tools:          agentToolNames,
		ToolHints:      toolHints,
		ToolGuidelines: toolGuidelines,
		Skills:         promptSkills,
		PigDocsPath:    codingagent.GetDocsPath(), PigReadmePath: codingagent.GetReadmePath(), PigExamplesPath: codingagent.GetExamplesPath(),
		AppendMode:   "append",
		ContextFiles: promptCtxFiles,
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
	build.SystemPromptOptions = extension.BuildSystemPromptOptions{
		CustomPrompt:       resolvedPrompts.custom,
		CustomPromptSet:    resolvedPrompts.customSet,
		SelectedTools:      append([]string{}, agentToolNames...),
		ToolSnippets:       toolHints,
		ToolGuidelines:     toolGuidelines,
		AppendSystemPrompt: resolvedPrompts.append,
		Cwd:                cwd,
		ContextFiles:       extContextFiles,
		Skills:             extensionPromptSkills(skillDefs),
	}
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

// toolSelection is the tool state the CLI flags resolve to.
type toolSelection struct {
	agentToolNames []string
	allowed        map[string]struct{}
	activeBuiltin  map[string]struct{}
	// initialActive is sdk.ts:274-276 initialActiveToolNames after the --exclude-tools filter.
	initialActive []string
	excluded      map[string]struct{}
	skipBuiltin   bool
	// modifiers are the --tools +name/-name entries, which a settings reload applies to the new defaultTools (agent-session.ts defaultToolModifiers).
	modifiers []string
	noTools   string
}

// resolveToolSelection resolves --tools, --no-tools, --no-builtin-tools and --exclude-tools, over the defaultTools setting and the registered built-in names, into the allowlist, the active set and the initial active names in the caller's order.
//
// upstream: sdk.ts:260-276 (allowedToolNames, excludedToolNames, initialActiveToolNames), agent-session.ts:3552-3562
func resolveToolSelection(flags Args, defaultTools, registryToolNames []string) toolSelection {
	agentToolNames := []string{"read", "bash", "edit", "write"}
	if defaultTools != nil {
		agentToolNames = defaultTools
	}
	allowed := map[string]struct{}(nil)
	var activeBuiltin map[string]struct{}
	// initialActive is sdk.ts:274-276 initialActiveToolNames before the --exclude-tools filter: --tools, else [] for --no-tools or --no-builtin-tools, else the defaultTools setting.
	var initialActive []string
	skipBuiltinTools := flags.NoBuiltinTools
	if flags.NoBuiltinTools {
		agentToolNames = []string{}
	}
	// A --tools list of only +name/-name entries changes the default selection instead of replacing it: the defaults (none under --no-tools or --no-builtin-tools) with the entries applied in order.
	// upstream: sdk.ts:279-295 (toolModifiers, selectedToolNames, allowedToolNames)
	var toolModifiers []string
	noTools := ""
	if slices.ContainsFunc(flags.Tools, codingagent.IsToolModifier) {
		toolModifiers = flags.Tools
	}
	// An explicit --tools list outranks --no-tools and --no-builtin-tools, as sdk.ts allowedToolNames = tools ?? (noTools === "all" ? [] : undefined) and initialActiveToolNames = tools ?? (noTools ? [] : defaults).
	switch {
	case toolModifiers != nil:
		base := agentToolNames
		if flags.NoTools || flags.NoBuiltinTools {
			base = []string{}
		}
		selected := codingagent.ApplyToolModifiers(base, toolModifiers)
		initialActive = slices.Clone(selected)
		agentToolNames = slices.Clone(selected)
		switch {
		case flags.NoTools:
			allowed = make(map[string]struct{}, len(selected))
			for _, name := range selected {
				allowed[name] = struct{}{}
			}
			skipBuiltinTools = false
		case flags.NoBuiltinTools:
			// noTools "builtin": the built-ins stay registered so the entries can activate them (sdk.ts:279-295).
			skipBuiltinTools = false
			noTools = "builtin"
		default:
			activeBuiltin = make(map[string]struct{}, len(selected))
			for _, name := range selected {
				activeBuiltin[name] = struct{}{}
			}
		}
	case flags.Tools != nil:
		allowed = make(map[string]struct{}, len(flags.Tools))
		for _, t := range flags.Tools {
			allowed[t] = struct{}{}
		}
		// Entries are tool names or `*` patterns (sdk.ts, mcp-servers.ts createToolNameMatcher).
		// The active built-ins keep the --tools order; the built-ins a pattern matches follow in registry order (agent-session.ts:3552-3562).
		named := extension.ToolNameMatcher(allowed)
		filtered := make([]string, 0, len(flags.Tools))
		for _, n := range flags.Tools {
			if slices.Contains(registryToolNames, n) && !slices.Contains(filtered, n) {
				filtered = append(filtered, n)
			}
		}
		for _, n := range registryToolNames {
			if named(n) && !slices.Contains(filtered, n) {
				filtered = append(filtered, n)
			}
		}
		agentToolNames = filtered
		initialActive = slices.Clone(flags.Tools)
		skipBuiltinTools = false
	case flags.NoTools:
		allowed = make(map[string]struct{})
		agentToolNames = []string{}
		skipBuiltinTools = false
		initialActive = []string{}
	default:
		initialActive = slices.Clone(agentToolNames)
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
		isExcluded := extension.ToolNameMatcher(excludedTools)
		filtered := agentToolNames[:0]
		for _, n := range agentToolNames {
			if !isExcluded(n) {
				filtered = append(filtered, n)
			}
		}
		agentToolNames = filtered
		initialActive = slices.DeleteFunc(initialActive, isExcluded)
	}
	return toolSelection{agentToolNames: agentToolNames, allowed: allowed, activeBuiltin: activeBuiltin, initialActive: initialActive, excluded: excludedTools, skipBuiltin: skipBuiltinTools, modifiers: toolModifiers, noTools: noTools}
}
