package cli

// Ports packages/coding-agent/src/core/agent-session.ts reload (3575-3625) and _buildRuntime (3547-3567) as print, JSON and RPC mode run them: print-mode.ts:97-99 and rpc-mode.ts:341-343 bind the command context's reload to session.reload().

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/ctxowner"
)

// headlessRebind binds a Session's replacement extension runner to its mode and emits session_start for event, as the mode's rebindSession does after reload.
type headlessRebind func(ctx context.Context, event extension.SessionStartEvent) error

// headlessReload is AgentSession.reload for one build's Session. call is the context of the extension call that asked for the reload; owner is the mode's process context, which the reloaded extension processes live under.
type headlessReload func(call, owner context.Context, session *coding.Session, rebind headlessRebind) error

// reloadHeadless runs AgentSession.reload for a headless Session over its build. The old extensions receive session_shutdown with reason reload; the settings, skills, prompt templates, context files and system prompt files are read again; the extension host loads every extension again; a new runner replaces the old one, which goes stale; and the tools newly added to defaultTools activate. rebind then binds the new runner to the mode and emits session_start with reason reload. An error returns to the extension that called ctx.reload(), as Pi's rejected promise does.
//
// A failure before the new runner exists leaves the previous runner stale: Pi invalidates it right after session_shutdown (agent-session.ts:3580) and keeps it when a later step throws, so every captured pi and ctx then throws the stale message.
func (b *cliRuntimeBuilder) reloadHeadless(ctx, owner context.Context, build *cliBuild, session *coding.Session, rebind headlessRebind) (err error) {
	previous := session.ExtensionRunner()
	if previous != nil && previous.HasHandlers(codingagent.EventSessionShutdown) {
		if _, err := previous.Emit(ctx, extension.SessionShutdownEvent{Type: codingagent.EventSessionShutdown, Reason: "reload"}); err != nil {
			return err
		}
	}
	// The old runner goes stale with the extension processes it fronts when the reload fails before the rebuild; the rebuild invalidates it itself. A call already running, such as the ctx.reload() that rejects with err, keeps its result (Host.Invalidate).
	rebuilt := false
	defer func() {
		if err == nil || rebuilt || previous == nil {
			return
		}
		previous.Invalidate("")
		if build.Host != nil {
			build.Host.Invalidate(previous.StaleMessage())
		}
	}()
	session.ReloadSettings()
	// The tools the settings reload staged activate in the runtime rebuild below; any other exit drops them (agent-session.ts:3598-3609 keeps them in a local of reload()).
	defer session.DiscardAddedDefaultTools()
	if err := b.reloadBuildResources(build); err != nil {
		return err
	}
	extensions, err := b.reloadBuildExtensions(ctxowner.WithValuesOf(owner, ctx), build)
	if err != nil {
		return err
	}
	rebuilt = true
	if _, err := session.ReloadExtensions(extensions, build.Runtime); err != nil {
		return fmt.Errorf("tool reload: %w", err)
	}
	return rebind(ctx, extension.SessionStartEvent{Type: codingagent.EventSessionStart, Reason: "reload"})
}

// reloadBuildResources is DefaultResourceLoader.reload for a build: it resolves the configured resource entries again and reads the skills, prompt templates, context files and system prompt files they select (resource-loader.ts reload). A resolution error leaves the previous resources in place.
func (b *cliRuntimeBuilder) reloadBuildResources(build *cliBuild) error {
	snapshot := reloadResourceSnapshotProvider(build.CWD, b.agentDir, build.Services.SettingsManager(), build.ResourceFlags, build.SkillScopes)()
	if snapshot.Err != nil {
		return snapshot.Err
	}
	build.PromptPaths, build.ThemePaths, build.SkillInputs = snapshot.PromptPaths, snapshot.ThemePaths, snapshot.SkillPaths
	if snapshot.ResourceSourceInfo != nil {
		build.RPCSourceInfo = snapshot.ResourceSourceInfo
	}
	return b.loadPromptResources(build, build.AgentToolNames, snapshot.ResourceSourceInfo)
}

// reloadBuildExtensions loads the build's extensions again and returns them in load order. The host reloads the file extensions on its own transaction; the built-in extensions load after them (resource-loader.ts:703-735). owner is the context the reloaded extension processes live under: a call's context ends with its command, which must not stop the extensions its reload started.
func (b *cliRuntimeBuilder) reloadBuildExtensions(owner context.Context, build *cliBuild) ([]extension.Extension, error) {
	if host := build.Host; host != nil {
		// A reload recompiles out-of-tree extensions, so stage the embedded SDKs first, as startup does.
		if err := pigsdk.EnsureSynced(codingagent.ConfigRoot()); err != nil {
			fmt.Fprintf(os.Stderr, "extension SDK staging: %v\n", err)
		}
		// Pi flushes the reloaded factories' provider registrations after every factory has finished, so no Provider callback runs while a later extension still loads.
		release := build.Services.Registry().HoldRegistrationRefresh()
		reloaded, err := host.Reload(owner)
		release()
		if err != nil {
			return nil, fmt.Errorf("extension reload: %w", err)
		}
		build.SubprocessExtensions = reloaded
	}
	if build.ReloadBuiltinExtensions != nil {
		build.BuiltinExtensions = build.ReloadBuiltinExtensions()
	}
	build.Extensions = codingagent.ExtensionsInLoadOrder(build.SubprocessExtensions, build.BuiltinExtensions)
	return build.Extensions, nil
}
