package cli

// Ports packages/coding-agent/src/core/resource-loader.ts DefaultResourceLoader.reload for a CLI build: the resource loader of a print, JSON or RPC Session reloads through these (cliResourceLoader.ReloadWith).

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

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
	build.Extensions = codingagent.ExtensionsInLoadOrderWithExplicit(build.SubprocessExtensions, build.BuiltinExtensions, build.ExplicitBuiltinPaths)
	return build.Extensions, nil
}
