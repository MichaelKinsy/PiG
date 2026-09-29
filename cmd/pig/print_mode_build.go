package main

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// printStartup carries the startup-only inputs of the first print-mode Session.
type printStartup struct {
	Manager     *coding.SessionManager
	ResumePath  string
	SessionName string
	SessionDir  string
	NoSession   bool
	SessionID   string
	CWDOverride *string
}

// printHost builds print and JSON mode's runtime inputs from a build. A replacement Session rebuilds through the same builder for its destination cwd.
func (b *cliRuntimeBuilder) printHost(build *cliBuild, startup *printStartup) printModeRuntime {
	promptResult := codingagent.LoadPromptTemplates("", "", build.PromptPaths...)
	registryAllowed, registryExcluded := toolRegistryFilters(build.Flags)
	host := printModeRuntime{
		UnknownFlags: build.Flags.UnknownFlags,
		Commands: headlessCommandCatalog{
			promptTemplates: promptResult.Templates,
			skills:          rpcResolvedSkills(build.skills(), b.activePiglet),
			cwd:             build.CWD,
			agentDir:        b.agentDir,
			sourceInfo:      build.SkillCatalog.SourceInfo,
			llama:           build.Llama,
		},
		ToolRegistryAllowed:  registryAllowed,
		ToolRegistryExcluded: registryExcluded,
		Services:             build.Services,
		Extensions:           build.Extensions,
		Bridge:               build.Bridge,
		Session: coding.SessionStartOptions{
			ScopedModels:          extensionScopedModels(build.Services, build.Settings.EnabledModels),
			Model:                 build.Model,
			ThinkingLevel:         ai.ThinkingLevel(build.Flags.Thinking),
			SystemPrompt:          build.SystemPrompt,
			SystemPromptSections:  build.SystemPromptSections,
			ResourceLoader:        coding.NoResources, // the CLI expands skills and prompts itself
			AllowedTools:          build.Allowed,
			ActiveBuiltinTools:    build.ActiveBuiltin,
			ExcludedTools:         build.ExcludedTools,
			SkipBuiltinTools:      build.SkipBuiltinTools,
			BeforeToolCall:        build.BeforeToolCall,
			SystemPromptResources: sessionPromptResources(build.ResolvedPrompts, build.ContextFiles, build.skills()),
		},
		SystemPromptSections: func(skills []*codingagent.SkillDef) ai.OrderedSections {
			options := build.PromptOptions
			options.Skills = promptSkillsFor(skills)
			return prompts.BuildSystemPromptSections(options)
		},
		SystemPromptResources: func(skills []*codingagent.SkillDef) *coding.SystemPromptResources {
			return sessionPromptResources(build.ResolvedPrompts, build.ContextFiles, skills)
		},
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
	if startup != nil {
		host.Session.SessionManager = startup.Manager
		host.Session.NoSession = startup.NoSession
		host.Session.SessionID = startup.SessionID
		host.Session.SessionDir = startup.SessionDir
		host.Session.CWDOverride = startup.CWDOverride
		host.ResumePath = startup.ResumePath
		host.SessionName = startup.SessionName
	}
	host.Rebuild = func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (printModeRuntime, error) {
		next, err := b.rebuild(ctx, options)
		if err != nil {
			return printModeRuntime{}, err
		}
		return b.printHost(next, nil), nil
	}
	return host
}

// rebuild constructs the destination cwd's build for a replacement Session.
func (b *cliRuntimeBuilder) rebuild(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (*cliBuild, error) {
	manager := options.SessionManager
	in := cliBuildInput{
		CWD:        options.CWD,
		Manager:    manager,
		Continuing: len(codingagent.BuildSessionContext(manager.GetBranch()).Messages) > 0,

		ProjectTrustUI: options.ProjectTrustUI,
	}
	build, err := b.buildResources(ctx, in)
	if err != nil {
		return nil, err
	}
	// A replacement whose extensions fail to load still starts, as Pi's does: the load errors stay in the extension host for the mode to list.
	if err := b.buildSession(ctx, build, in); err != nil {
		return nil, b.discardBuild(build, "build failed", err)
	}
	if build.ModelErr != nil {
		return nil, b.discardBuild(build, "model selection failed", cliCLIError("%v", build.ModelErr))
	}
	return build, nil
}

// discardBuild releases a build no Session owns and returns err.
func (b *cliRuntimeBuilder) discardBuild(build *cliBuild, reason string, err error) error {
	if build.Host != nil {
		build.Host.Shutdown(reason)
	}
	build.Services.Close()
	return err
}
