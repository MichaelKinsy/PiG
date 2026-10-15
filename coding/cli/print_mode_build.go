package cli

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
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

// printHost builds print and JSON mode's runtime inputs from a build; owner is the process context the extension processes a reload starts live under. A replacement Session rebuilds through the same builder for its destination cwd.
func (b *cliRuntimeBuilder) printHost(owner context.Context, build *cliBuild, startup *printStartup) printModeRuntime {
	registryAllowed, registryExcluded := toolRegistryFilters(build.Flags)
	host := printModeRuntime{
		ToolRegistryAllowed:  registryAllowed,
		ToolRegistryExcluded: registryExcluded,
		Services:             build.Services,
		Extensions:           build.Extensions,
		Bridge:               build.Bridge,
		Host:                 build.Host,
		Runtime:              build.Runtime,
		Session: coding.SessionStartOptions{
			// The Session builds its default system prompt from this loader, as AgentSession._rebuildSystemPrompt does.
			ResourceLoader:         b.buildResourceLoader(owner, build),
			ScopedModels:           extensionScopedModels(build.Services, build.Settings.EnabledModels),
			Model:                  build.Model,
			ThinkingLevel:          ai.ModelThinkingLevel(build.Flags.Thinking),
			AllowedTools:           build.Allowed,
			InitialActiveToolNames: build.InitialActiveToolNames,
			DefaultToolModifiers:   build.DefaultToolModifiers,
			NoTools:                build.NoTools,
			ExcludedTools:          build.ExcludedTools,
			SkipBuiltinTools:       build.SkipBuiltinTools,
			BeforeToolCall:         build.BeforeToolCall,
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
		return b.printHost(owner, next, nil), nil
	}
	return host
}

// rebuild constructs the destination cwd's build for a replacement Session.
func (b *cliRuntimeBuilder) rebuild(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (*cliBuild, error) {
	manager := options.SessionManager
	in := cliBuildInput{
		CWD:        options.CWD,
		Manager:    manager,
		Continuing: len(manager.BuildSessionContext().Messages) > 0,
	}
	if options.ProjectTrustContext != nil {
		// The replacement's trust prompt uses the live extension UI the context carries (agent-session-runtime.ts:219).
		in.ProjectTrustUI, _ = options.ProjectTrustContext.UI.(extension.UIContext)
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
