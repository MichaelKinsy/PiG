package cli

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// interactiveRuntime adapts *coding.Runtime to the Session replacement contract of InteractiveMode.
type interactiveRuntime struct{ rt *coding.Runtime }

func (r interactiveRuntime) NewSession(ctx context.Context, options *extension.NewSessionOptions) (extension.CancelledResult, error) {
	return r.rt.NewSession(ctx, options)
}

func (r interactiveRuntime) SwitchSession(ctx context.Context, path, cwdOverride string, projectTrustUI func(cwd string) extension.ProjectTrustContext, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	switchOptions := &coding.SwitchSessionOptions{CwdOverride: cwdOverride, ProjectTrustContextFactory: projectTrustUI}
	if options != nil {
		switchOptions.WithSession = options.WithSession
	}
	return r.rt.SwitchSession(ctx, path, switchOptions)
}

func (r interactiveRuntime) Fork(ctx context.Context, entryID string, options *extension.ForkOptions) (codingagent.InteractiveForkResult, error) {
	result, err := r.rt.Fork(ctx, entryID, options)
	return codingagent.InteractiveForkResult{Cancelled: result.Cancelled, SelectedText: result.SelectedText}, err
}

func (r interactiveRuntime) ImportFromJsonl(ctx context.Context, path, cwdOverride string) (extension.CancelledResult, error) {
	if cwdOverride == "" {
		return r.rt.ImportFromJsonl(ctx, path)
	}
	return r.rt.ImportFromJsonl(ctx, path, cwdOverride)
}

func (r interactiveRuntime) ExtensionCommandActions(session codingagent.InteractiveSessionHandle) extension.CommandActions {
	current, ok := session.(*coding.Session)
	if !ok {
		return extension.CommandActions{}
	}
	return r.rt.ExtensionCommandActions(current)
}

func (r interactiveRuntime) EmitQuitShutdown() { r.rt.EmitQuitShutdown() }

// EmitQuitShutdownAfterStop is the dispose of the quit by keys, which runs after the TUI stopped (Pi: this.stop(), then `await this.runtimeHost.dispose()`, interactive-mode.ts shutdown).
func (r interactiveRuntime) EmitQuitShutdownAfterStop() {
	r.rt.EmitQuitShutdownAfterInputEnd(func() { exitAtDrainedQuit(r.rt.Session()) })
}

// exitAtDrainedQuit ends the process as Pi's ends when its event loop empties under the pending dispose of a quit: status 0, with no session_shutdown and no resume hint (the dispose never returns).
// pig additive (D19): Pi's extensions end with its process. Here the Session stops persisting first, because ending the extension processes fails the pending request with an error of the host's own, which Pi's process never produces; then the extension hosts end by their normal shutdown (stopStartupExtensions, the close main defers), and TerminateProcesses ends what a replaced Session's host still runs, as the RPC exit does.
func exitAtDrainedQuit(session *coding.Session) {
	// The report arrives on a goroutine of the extension host, which the host's shutdown joins, so the exit runs apart from it.
	go func() { //portlint:allow golifetime the goroutine ends in the process exit (exitProcess), as Pi's loop-drain exit ends the process
		session.Dispose()
		stopStartupExtensions()
		if host := session.ExtensionHost(); host != nil {
			host.TerminateProcesses()
		}
		// Pi interactive-mode.ts:4569-4571 (31b407dc63, #11889) kills tracked detached shells right before process.exit:
		// a drained quit skips InteractiveMode.Stop, so a running `!` command would otherwise outlive exitProcess(0).
		tools.KillTrackedDetachedChildren()
		exitProcess(0)
	}()
}

func (r interactiveRuntime) SetBeforeQuitDispose(settle func()) { r.rt.SetBeforeQuitDispose(settle) }

func (r interactiveRuntime) SetBeforeSessionReplacement(drain func(context.Context) error) {
	r.rt.SetBeforeSessionReplacement(drain)
}

func (r interactiveRuntime) SetBeforeSessionInvalidate(hook func()) {
	r.rt.SetBeforeSessionInvalidate(hook)
}

func (r interactiveRuntime) SetRebindSession(rebind func(context.Context, codingagent.InteractiveSessionHandle) error) {
	r.rt.SetRebindSession(func(ctx context.Context, session *coding.Session) error { return rebind(ctx, session) })
}

func (r interactiveRuntime) TakeUnreportedDiagnostics() []codingagent.AgentSessionRuntimeDiagnostic {
	return r.rt.TakeUnreportedDiagnostics()
}

// interactiveStart is the Session start options of an interactive build.
func interactiveStart(build *cliBuild) coding.SessionStartOptions {
	return coding.SessionStartOptions{
		ScopedModels:           extensionScopedModels(build.Services, build.Settings.EnabledModels),
		Model:                  build.Model,
		ThinkingLevel:          ai.ModelThinkingLevel(build.Flags.Thinking),
		SystemPrompt:           build.SystemPrompt,
		SystemPromptSections:   build.SystemPromptSections,
		SystemPromptResources:  sessionPromptResources(build.ResolvedPrompts, build.ContextFiles, build.skills()),
		ResourceLoader:         coding.NoResources, // the CLI expands skills and prompts itself
		AllowedTools:           build.Allowed,
		ToolScope:              build.ToolScope,
		InitialActiveToolNames: build.InitialActiveToolNames,
		DefaultToolModifiers:   build.DefaultToolModifiers,
		NoTools:                build.NoTools,
		ExcludedTools:          build.ExcludedTools,
		SkipBuiltinTools:       build.SkipBuiltinTools,
		BeforeToolCall:         build.BeforeToolCall,
	}
}

// interactiveInputs builds interactive mode's Session inputs from a build.
func (b *cliRuntimeBuilder) interactiveInputs(build *cliBuild, startup coding.SessionStartOptions) cliSessionInputs {
	start := interactiveStart(build)
	start.SessionManager = startup.SessionManager
	start.ResumePath = startup.ResumePath
	start.SessionDir = startup.SessionDir
	start.SessionID = startup.SessionID
	start.NoSession = startup.NoSession
	start.CWDOverride = startup.CWDOverride
	return cliSessionInputs{
		Services: build.Services, Extensions: build.Extensions, Start: start, Host: build.Host, Bridge: build.Bridge, Runtime: build.Runtime,
		Diagnostics:          build.runtimeDiagnostics(),
		ModelFallbackMessage: build.ModelFallbackMessage,
		Invalidate: func(message string) {
			if build.Host != nil {
				build.Host.Invalidate(message)
			}
		},
		Release: func(reason string) {
			if build.Host != nil {
				build.Host.Shutdown(reason)
			}
			build.Services.Close()
		},
	}
}

// interactiveReplacement carries a replacement build's cwd-bound options to InteractiveMode.
func (b *cliRuntimeBuilder) interactiveReplacement(build *cliBuild, session *coding.Session, requestAuth *codingagent.RequestAuthRuntime) codingagent.InteractiveReplacement {
	services := build.Services
	flags := build.Flags
	interactiveRegistryAllowed, _ := toolRegistryFilters(flags)
	replacement := codingagent.InteractiveReplacement{
		CWD:                     build.CWD,
		SessionDir:              session.Inner().GetSessionDir(),
		Settings:                build.Settings,
		SettingsManager:         services.SettingsManager(),
		ModelRegistry:           services.Registry().ModelRegistry,
		SystemPrompt:            build.SystemPrompt,
		SystemPromptOptions:     build.SystemPromptOptions,
		AllowedTools:            build.Allowed,
		ActiveBuiltinTools:      build.ActiveBuiltin,
		InitialActiveToolNames:  build.InitialActiveToolNames,
		ExcludedTools:           build.ExcludedTools,
		ToolRegistryAllowed:     interactiveRegistryAllowed,
		NoBuiltinTools:          build.SkipBuiltinTools,
		PromptPaths:             build.PromptPaths,
		CLIPromptTemplatePaths:  additionalPromptTemplatePaths(build.ResourceFlags),
		ThemePaths:              build.ThemePaths,
		SkillPaths:              build.SkillInputs,
		Skills:                  build.skills(),
		SkillDiagnostics:        build.SkillLoad.Diagnostics,
		ContextFiles:            build.ContextFiles,
		SystemPromptSourcePaths: build.ResolvedPrompts.sourcePaths,
		RebuildSystemPrompt:     systemPromptRebuilder(build.CWD, b.agentDir, build.ProjectTrusted, flags, build.AgentToolNames),
		ResourceSourceInfo:      resourceSourceInfoProvider(build.CWD, b.agentDir, services.SettingsManager(), flags, build.SourceResolver.Resolve),
		ReloadResources:         reloadResourceSnapshotProvider(build.CWD, b.agentDir, services.SettingsManager(), build.ResourceFlags, build.SkillScopes),
		ExtensionRunner:         session.ExtensionRunner(),
		SessionStartEvent:       session.StartEvent(),
		ExtensionConflicts:      codingagent.DetectExtensionConflicts(build.Extensions),
		ExtensionWarnings:       build.ExtensionWarnings,
		BuiltinExtensions:       build.BuiltinExtensions,
		ExplicitBuiltinPaths:    build.ExplicitBuiltinPaths,
		ReloadBuiltinExtensions: build.ReloadBuiltinExtensions,
		ModelBuilder:            func(spec string) (*ai.Model, error) { return coding.BuildModel(spec, services) },
		ModelBinder:             session.ModelRuntime().BindModelObject,
		ModelLookup:             session.ModelRuntime().GetModel,
		ModelCatalog:            session.ModelRuntime().GetModels,
		ModelClassify:           session.ModelRuntime().Classify,
		ModelGenerateImages:     session.ModelRuntime().GenerateImages,
		RequestAuthRuntime:      requestAuth,
	}
	// A typed nil pointer would make these interfaces non-nil.
	if build.Bridge != nil {
		replacement.SubprocessUIBridge = build.Bridge
	}
	if build.Host != nil {
		replacement.SubprocessHost = build.Host
	}
	return replacement
}
