package cli

// The extension-set harness: the pre-trust pass, the trust callback and the final pass of resource-loader.ts reload over coding/cli's own pieces
// (collectExtensionConfigs, loadFinalSubprocessExtensions, the factory loader). cmd/pig sequences the same pieces inline in cli_runtime_build.go
// and package_command_trust.go; only these tests drive this form.

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/extensionset"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// extensionSetHarness is the loader with the settings the harness's file load needs; production loads files in cli_runtime_build.go.
type extensionSetHarness struct {
	extensionSetLoader
	AgentDir  string
	Mode      extension.ExtensionMode
	Registry  *codingagent.ModelRegistry
	Resolvers []extsource.ResolveFunc
}

// extensionSetResult is types.ts LoadExtensionsResult for the extensions a reload selected.
type extensionSetResult struct {
	Extensions []extension.Extension
	Errors     []extensionSetError
	Warnings   []extensionSetWarning
	// Host owns the subprocess extensions; nil when none loaded.
	Host *subprocess.Host
	// startup owns the host until Close.
	startup *startupExtensionSet
}

// Close shuts down the subprocess host that owns the loaded file extensions.
func (r *extensionSetResult) Close() {
	if r != nil {
		r.startup.close()
	}
}

// Reload rediscovers the extension set. With resolveProjectTrust it first loads the pre-trust set with the project untrusted and
// hands it to the callback; the final set then loads for the trust the callback returns (resource-loader.ts reload). The caller
// closes the result.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:531-581 (reload) and :585-601 (loadProjectTrustExtensions).
func (l *extensionSetHarness) Reload(ctx context.Context, resolveProjectTrust func(pre extensionSetResult) (bool, error)) (*extensionSetResult, error) {
	startup := &startupExtensionSet{}
	var pre *extensionSetResult
	if resolveProjectTrust != nil {
		// Force untrusted project settings for the bootstrap pass. This keeps project-local extensions and packages out while
		// still loading user and temporary CLI extensions.
		l.Settings.SetProjectTrusted(false)
		l.Settings.Reload()
		var err error
		if pre, err = l.loadCurrentExtensionSet(ctx, startup, true); err != nil {
			startup.close()
			return nil, err
		}
		trusted, err := resolveProjectTrust(*pre)
		if err != nil {
			startup.close()
			return nil, err
		}
		l.Settings.SetProjectTrusted(trusted)
	}
	l.Settings.Reload()
	result, err := l.loadFinalExtensionSet(ctx, startup, pre)
	if err != nil {
		startup.close()
		return nil, err
	}
	return result, nil
}

// configs are the file extensions of the current settings and flags. A pre-trust pass reads only the user scope.
func (l *extensionSetHarness) configs(preTrust bool) []subprocess.ExtConfig {
	var scopes *[]string
	if preTrust {
		scopes = &[]string{"user"}
	}
	return collectExtensionConfigs(l.CWD, l.AgentDir, l.Settings, l.Flags, scopes, l.Resolvers...)
}

// loadFiles loads the file extensions with the subprocess Host that startup owns.
func (l *extensionSetHarness) loadFiles(ctx context.Context, startup *startupExtensionSet, configs []subprocess.ExtConfig) ([]extension.Extension, []extensionSetError) {
	loaded, _, _, errs := loadFinalSubprocessExtensions(ctx, l.CWD, l.Mode, l.Registry, configs, nil, nil, startup, nil)
	return loaded, extensionSetErrors(errs)
}

func extensionSetErrors(errs []error) []extensionSetError {
	out := make([]extensionSetError, 0, len(errs))
	for _, err := range errs {
		if loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err); ok && loadErr.Path != "" {
			out = append(out, extensionSetError{Path: loadErr.Path, Error: loadErr.Err.Error()})
			continue
		}
		out = append(out, extensionSetError{Error: err.Error()})
	}
	return out
}

// loadCurrentExtensionSet loads the file extensions of the current settings, then the inline extensions. Built-in extensions wait
// for the final pass: project settings can disable them, and a loaded extension cannot be unloaded.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:655-701 (loadCurrentExtensionSet).
func (l *extensionSetHarness) loadCurrentExtensionSet(ctx context.Context, startup *startupExtensionSet, includeInline bool) (*extensionSetResult, error) {
	configs := l.configs(true)
	packageWarnings, err := extensionPackageWarnings(configs)
	if err != nil {
		return nil, err
	}
	files, errs := l.loadFiles(ctx, startup, configs)
	result := &extensionSetResult{Extensions: files, Errors: errs, Warnings: packageWarnings, Host: startup.host, startup: startup}
	if !includeInline {
		return result, nil
	}
	inline, inlineErrs := l.loadInlineExtensions()
	result.Extensions = append(result.Extensions, inline...)
	result.Errors = append(result.Errors, inlineErrs...)
	var replacementWarnings []extensionSetWarning
	result.Extensions = omitReplacedExtensions(result.Extensions, &replacementWarnings)
	result.Warnings = mergeExtensionWarnings(result.Warnings, replacementWarnings)
	return result, nil
}

// loadFinalExtensionSet loads what the pre-trust pass did not: file extensions of the trusted project, the built-in extensions the
// settings and flags enable, and, without a pre-trust pass, the inline extensions. Built-in extensions follow the file extensions.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:703-781 (loadExtensionPaths, loadFinalExtensionSet).
func (l *extensionSetHarness) loadFinalExtensionSet(ctx context.Context, startup *startupExtensionSet, pre *extensionSetResult) (*extensionSetResult, error) {
	configs := l.configs(false)
	packageWarnings, err := extensionPackageWarnings(configs)
	if err != nil {
		return nil, err
	}
	files, errs := l.loadFiles(ctx, startup, configs)
	var preErrors []extensionSetError
	var preWarnings []extensionSetWarning
	if pre != nil {
		preErrors, preWarnings = pre.Errors, pre.Warnings
	}

	// Explicit `-e builtin:<name>` paths come first, as the CLI paths precede the resolved ones; the enabled settings paths follow.
	builtinPaths := enabledBuiltinExtensionPaths(l.Settings, l.Flags, extensionset.BuiltinNames(l.Inline))
	explicit := 0
	for _, path := range builtinPaths {
		if slices.Contains(l.Flags.Extensions, path) {
			explicit++
		}
	}
	builtins, builtinErrs := l.loadBuiltinExtensions(builtinPaths)

	var inline []extension.Extension
	var inlineErrs []extensionSetError
	if pre != nil {
		for _, ext := range pre.Extensions {
			if strings.HasPrefix(ext.Path, "<inline:") {
				inline = append(inline, ext)
			}
		}
	} else {
		inline, inlineErrs = l.loadInlineExtensions()
	}

	ordered := make([]extension.Extension, 0, len(files)+len(builtins)+len(inline))
	ordered = append(ordered, builtins[:min(explicit, len(builtins))]...)
	ordered = append(ordered, files...)
	ordered = append(ordered, builtins[min(explicit, len(builtins)):]...)
	ordered = append(ordered, inline...)

	var replacementWarnings []extensionSetWarning
	result := &extensionSetResult{
		Extensions: omitReplacedExtensions(ordered, &replacementWarnings),
		Errors:     slices.Concat(preErrors, errs, builtinErrs, inlineErrs),
		Warnings:   mergeExtensionWarnings(preWarnings, replacementWarnings),
		Host:       startup.host,
		startup:    startup,
	}
	result.Warnings = mergeExtensionWarnings(result.Warnings, packageWarnings)
	return result, nil
}
