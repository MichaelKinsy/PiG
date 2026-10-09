package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// packageCommandRuntimeOptions are the extension factories Main passes to the package and config commands: the built-in extensions, then MainOptions.ExtensionFactories (main.ts:575-610). It does not discover or load linked code.
// Ports packages/coding-agent/src/package-manager-cli.ts:746-748.
type packageCommandRuntimeOptions struct {
	extensionFactories []extension.InlineExtension
}

// Ports packages/coding-agent/src/package-manager-cli.ts (createCommandSettingsManager).
// Updates consult saved trust only; other package commands await global trust handlers before reading project settings.
func createPackageCommandSettings(ctx context.Context, cwd, agentDir string, opts *packageCLIOptions, runtimeOptions ...packageCommandRuntimeOptions) (*codingagent.SettingsManager, error) {
	settings := codingagent.NewSettingsManagerWithOptions(cwd, agentDir, codingagent.SettingsManagerCreateOptions{ProjectTrusted: new(false)})
	store := codingagent.NewProjectTrustStore(agentDir)
	if opts.command == packageUpdate {
		decision := opts.projectTrustOverride
		if decision == nil {
			var err error
			decision, err = store.Get(cwd)
			if err != nil {
				return nil, err
			}
		}
		settings.SetProjectTrusted(decision != nil && *decision)
		return settings, nil
	}

	loaded := &startupExtensionSet{}
	defer loaded.close()
	var runner *inproc.Runner
	ui := extension.NoopUIContext
	mode := processAppMode(Args{})
	if mode == appModeInteractive {
		ui = newStartupTrustUI(codingagent.StartupUIOptions{AgentDir: agentDir, Settings: settings.Get()})
	}
	if opts.projectTrustOverride == nil && codingagent.HasTrustRequiringProjectResources(cwd) {
		// Upstream resource-loader.ts:loadProjectTrustExtensions resolves global packages before loading trust handlers, with project settings still disabled.
		for _, pkg := range packagemanager.ConfiguredPackagesForResolution(cwd, settings.AgentDir(), settings) {
			if pkg.InstalledPath == "" && packagemanager.DetectSourceKind(pkg.Source.Source) != "local" && !packagemanager.IsOfflineModeEnabled() {
				if err := packagemanager.InstallPackageArtifacts(cwd, settings.AgentDir(), settings, pkg.Source, false, nil); err != nil {
					return nil, err
				}
			}
		}
		userScope := []string{"user"}
		resolver := newStartupExtensionSourceResolver(nil)
		configs := collectExtensionConfigs(cwd, agentDir, settings, Args{}, &userScope, resolver.Resolve)
		var pathExtensions []extension.Extension
		if len(configs) > 0 {
			stageExtensionSDKsAtStartup(ctx, codingagent.ConfigRoot(), os.Stderr, startupSDKLockTimeout)
			extensions, _, bridge, loadErrors := loadFinalSubprocessExtensions(ctx, cwd, mode.extensionMode(), codingagent.NewModelRegistry(agentDir), configs, nil, nil, loaded)
			for _, err := range loadErrors {
				fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			}
			if bridge != nil {
				bridge.SetUIContext(ui)
			}
			pathExtensions = extensions
		}
		var inlineExtensions []extension.Extension
		factoryRuntime := extension.CreateExtensionRuntime()
		if len(runtimeOptions) > 0 {
			// resource-loader.ts loadProjectTrustExtensions loads the inline extensions (loadExtensionFactories), named <inline:name> or <inline:N>.
			loader := &extensionSetLoader{CWD: cwd, AgentDir: agentDir, Settings: settings, Inline: runtimeOptions[0].extensionFactories, Runtime: factoryRuntime}
			var loadErrs []extensionSetError
			inlineExtensions, loadErrs = loader.loadInlineExtensions()
			for _, loadErr := range loadErrs {
				fmt.Fprintf(os.Stderr, "Warning: Failed to load extension %q: %s\n", loadErr.Path, loadErr.Error)
			}
		}
		extensions := codingagent.ExtensionsInLoadOrder(pathExtensions, inlineExtensions)
		if len(extensions) > 0 {
			runner = inproc.NewRunner(extensions, cwd, factoryRuntime)
		}
	}
	trusted, err := resolveProjectTrusted(ctx, projectTrustResolutionOptions{
		CWD: cwd, Store: store, Override: opts.projectTrustOverride,
		Default: settings.GetGlobalSettings().DefaultProjectTrust, Runner: runner, UI: ui,
		Mode: mode, HasUI: mode == appModeInteractive,
		OnExtensionError: func(message string) { fmt.Fprintf(os.Stderr, "Warning: %s\n", message) },
	})
	if err != nil {
		return nil, err
	}
	settings.SetProjectTrusted(trusted)
	return settings, nil
}
