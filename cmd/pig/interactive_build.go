package main

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// interactiveRuntime adapts *coding.Runtime to the Session replacement contract of InteractiveMode.
type interactiveRuntime struct{ rt *coding.Runtime }

func (r interactiveRuntime) NewSession(ctx context.Context, options *extension.NewSessionOptions) (extension.CancelledResult, error) {
	return r.rt.NewSession(ctx, options)
}

func (r interactiveRuntime) SwitchSession(ctx context.Context, path, cwdOverride string, projectTrustUI func(cwd string) extension.UIContext, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	return r.rt.SwitchSessionWithProjectTrust(ctx, path, cwdOverride, projectTrustUI, options)
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

func (r interactiveRuntime) SetBeforeSessionReplacement(drain func(context.Context) error) {
	r.rt.SetBeforeSessionReplacement(drain)
}

func (r interactiveRuntime) SetBeforeSessionInvalidate(hook func()) {
	r.rt.SetBeforeSessionInvalidate(hook)
}

func (r interactiveRuntime) SetRebindSession(rebind func(context.Context, codingagent.InteractiveSessionHandle) error) {
	r.rt.SetRebindSession(func(ctx context.Context, session *coding.Session) error { return rebind(ctx, session) })
}

// interactiveStart is the Session start options of an interactive build.
func interactiveStart(build *cliBuild) coding.SessionStartOptions {
	return coding.SessionStartOptions{
		ScopedModels:          extensionScopedModels(build.Services, build.Settings.EnabledModels),
		Model:                 build.Model,
		ThinkingLevel:         ai.ThinkingLevel(build.Flags.Thinking),
		SystemPrompt:          build.SystemPrompt,
		SystemPromptSections:  build.SystemPromptSections,
		SystemPromptResources: sessionPromptResources(build.ResolvedPrompts, build.ContextFiles, build.skills()),
		ResourceLoader:        coding.NoResources, // the CLI expands skills and prompts itself
		AllowedTools:          build.Allowed,
		ActiveBuiltinTools:    build.ActiveBuiltin,
		ExcludedTools:         build.ExcludedTools,
		SkipBuiltinTools:      build.SkipBuiltinTools,
		BeforeToolCall:        build.BeforeToolCall,
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
		Services: build.Services, Extensions: build.Extensions, Start: start,
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
		ExcludedTools:           build.ExcludedTools,
		ToolRegistryAllowed:     interactiveRegistryAllowed,
		NoBuiltinTools:          build.SkipBuiltinTools,
		PromptPaths:             build.PromptPaths,
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
		BuiltinExtensions:       build.BuiltinExtensions,
		ReloadBuiltinExtensions: build.ReloadBuiltinExtensions,
		Llama:                   build.Llama,
		ModelLookup:             session.ModelRuntime().GetModel,
		ModelCatalog:            session.ModelRuntime().GetModels,
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
