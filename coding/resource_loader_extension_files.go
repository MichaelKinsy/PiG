package coding

// Ports the extension half of packages/coding-agent/src/core/resource-loader.ts reload (:513-600): the extension paths of the Packages and
// settings, and the additional extension paths, load as one set.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extensionset"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// loadExtensionFiles is the default [DefaultResourceLoaderOptions.LoadExtensions]. The paths are the additional extension paths, then, unless
// NoExtensions, the enabled extensions of the Packages and settings, merged without duplicates (resource-loader.ts:566-575). A `builtin:` path of a
// disabled built-in extension is dropped (:576-579); another `builtin:` path is an error, because this loader has no built-in extensions
// (:722-727). A Package whose package.json lists host-provided packages under dependencies adds a warning (:581, :66-93). A local additional
// path that does not exist is an error of the result (resource-loader.ts:545-551). The loader keeps the Extension Host of the load until the next load or Close.
func (l *DefaultResourceLoader) loadExtensionFiles(ctx context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
	manager := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: l.cwd, SettingsManager: l.settings})
	manager.AgentDir = l.agentDir
	cli, err := manager.ResolveExtensionSources(l.options.AdditionalExtensionPaths, false, true)
	if err != nil {
		return LoadExtensionsResult{}, fmt.Errorf("coding: %w", err)
	}
	enabled := enabledExtensionPaths(cli.Extensions)
	packageRoots := map[string]string{}
	if !l.options.NoExtensions {
		resolved, err := manager.Resolve(nil)
		if err != nil {
			return LoadExtensionsResult{}, fmt.Errorf("coding: %w", err)
		}
		enabled = append(append([]string{}, enabled...), enabledExtensionPaths(resolved.Extensions)...)
		for _, resource := range resolved.Extensions {
			packageRoots[resource.Path] = resource.Metadata.PackageRoot
		}
	}
	for _, resource := range cli.Extensions {
		if _, seen := packageRoots[resource.Path]; !seen {
			packageRoots[resource.Path] = resource.Metadata.PackageRoot
		}
	}
	paths, err := l.mergePaths(nil, enabled)
	if err != nil {
		return LoadExtensionsResult{}, err
	}
	paths = slices.DeleteFunc(paths, func(path string) bool {
		name, builtin := strings.CutPrefix(path, icodingagent.BuiltinPathPrefix)
		return builtin && slices.Contains(l.options.DisabledBuiltinExtensions, name)
	})
	var roots []string
	for _, path := range paths {
		roots = append(roots, packageRoots[path])
	}
	packageWarnings, err := icodingagent.ExtensionPackageWarnings(roots)
	if err != nil {
		return LoadExtensionsResult{}, fmt.Errorf("coding: %w", err)
	}
	var filePaths, builtinPaths []string
	for _, path := range paths {
		if strings.HasPrefix(path, icodingagent.BuiltinPathPrefix) {
			builtinPaths = append(builtinPaths, path)
		} else {
			filePaths = append(filePaths, path)
		}
	}
	configs := make([]subprocess.ExtConfig, 0, len(filePaths))
	for _, path := range filePaths {
		configs = append(configs, extensionPathConfigs(path)...)
	}
	// One runtime serves every extension of a reload: the Host adopts it and the factories register against it (resource-loader.ts passes
	// preTrustExtensions.runtime on). A pass with no PreTrust starts a new reload.
	if request.PreTrust == nil || l.factoryRuntime == nil {
		l.factoryRuntime = extension.CreateExtensionRuntime()
	}
	if l.eventBus == nil {
		l.eventBus = l.options.EventBus
	}
	if l.eventBus == nil {
		l.eventBus = extension.CreateEventBus()
	}
	factories := &extensionset.Loader{CWD: l.cwd, Inline: l.options.ExtensionFactories, Runtime: l.factoryRuntime, EventBus: l.eventBus}
	var result LoadExtensionsResult
	if request.PreTrust != nil && l.files.host != nil {
		// resource-loader.ts:747-790 loadFinalExtensionSet: the extensions the bootstrap pass loaded stay loaded, and a path it attempted, loaded or failed, is
		// not attempted again; only the other paths load, in the same Host so the extensions share one runtime.
		host := l.files.host
		loaded, errs := host.LoadFinalExtensionSet(ctx, configs, l.filesConfigs)
		result = LoadExtensionsResult{
			Extensions: append([]extension.Extension{}, loaded...),
			Errors:     slices.Concat(request.PreTrust.Errors, extensionLoadErrors(errs)),
			Warnings:   slices.Clone(request.PreTrust.Warnings),
			Runtime:    host.Runtime(),
			host:       host,
		}
	} else {
		l.closeExtensionFiles()
		result = loadExtensionPathsIn(ctx, filePaths, l.cwd, l.factoryRuntime)
		if request.PreTrust != nil {
			result.Errors = slices.Concat(request.PreTrust.Errors, result.Errors)
			result.Warnings = slices.Concat(request.PreTrust.Warnings, result.Warnings)
		}
	}
	// The built-in and inline extensions the bootstrap pass loaded stay loaded and its attempted paths are not tried again (resource-loader.ts:747-790).
	preloaded := map[string]extension.Extension{}
	attempted := map[string]bool{}
	var preInline []extension.Extension
	if request.PreTrust != nil {
		for _, ext := range request.PreTrust.Extensions {
			switch {
			case strings.HasPrefix(ext.Path, "<inline:"):
				preInline = append(preInline, ext)
			case strings.HasPrefix(ext.Path, icodingagent.BuiltinPathPrefix):
				preloaded[ext.Path] = ext
			}
		}
		for _, loadErr := range request.PreTrust.Errors {
			attempted[loadErr.Path] = true
		}
	}
	// The built-in paths load in path order, an unknown name and a failing factory each ending as its own error entry at its place (:722-742).
	var builtinsToLoad []string
	for _, path := range builtinPaths {
		if _, have := preloaded[path]; !have && !attempted[path] {
			builtinsToLoad = append(builtinsToLoad, path)
		}
	}
	builtins, builtinErrs := factories.LoadBuiltin(builtinsToLoad)
	for _, ext := range builtins {
		preloaded[ext.Path] = ext
	}
	for _, loadErr := range builtinErrs {
		result.Errors = append(result.Errors, ExtensionLoadError{Path: loadErr.Path, Error: loadErr.Error})
	}
	inline := preInline
	if request.PreTrust == nil {
		var inlineErrs []extensionset.LoadError
		inline, inlineErrs = factories.LoadInline()
		for _, loadErr := range inlineErrs {
			result.Errors = append(result.Errors, ExtensionLoadError{Path: loadErr.Path, Error: loadErr.Error})
		}
	}
	// Extensions follow the order of the paths, built-ins in place, then the inline ones (:765-775).
	files := map[string]extension.Extension{}
	for _, ext := range result.Extensions {
		files[icodingagent.CanonicalizePath(ext.ResolvedPath)] = ext
	}
	ordered := make([]extension.Extension, 0, len(result.Extensions)+len(preloaded)+len(inline))
	used := map[string]bool{}
	for _, path := range paths {
		if ext, ok := preloaded[path]; ok {
			ordered = append(ordered, ext)
		} else if ext, ok := files[icodingagent.CanonicalizePath(path)]; ok {
			ordered = append(ordered, ext)
			used[icodingagent.CanonicalizePath(path)] = true
		}
	}
	for _, ext := range result.Extensions {
		if !used[icodingagent.CanonicalizePath(ext.ResolvedPath)] {
			ordered = append(ordered, ext)
		}
	}
	var replacementWarnings []icodingagent.ExtensionWarning
	result.Extensions = extensionset.OmitReplaced(append(ordered, inline...), &replacementWarnings)
	result.Runtime = l.factoryRuntime
	var existing []icodingagent.ExtensionWarning
	for _, warning := range result.Warnings {
		existing = append(existing, icodingagent.ExtensionWarning{Path: warning.Path, Warning: warning.Warning})
	}
	result.Warnings = nil
	for _, warning := range icodingagent.MergeExtensionWarnings(icodingagent.MergeExtensionWarnings(existing, replacementWarnings), packageWarnings) {
		result.Warnings = append(result.Warnings, ExtensionLoadWarning{Path: warning.Path, Warning: warning.Warning})
	}
	l.files, l.filesConfigs = result, configs
	for _, path := range l.options.AdditionalExtensionPaths {
		if request.Bootstrap || !icodingagent.IsLocalPath(path) {
			continue
		}
		resolved, err := l.resolveResourcePath(path)
		if err != nil {
			return LoadExtensionsResult{}, err
		}
		if _, statErr := os.Stat(resolved); statErr != nil {
			result.Errors = append(result.Errors, ExtensionLoadError{Path: resolved, Error: "Extension path does not exist: " + resolved})
		}
	}
	return result, nil
}

// extensionLoadErrors maps the errors of a Host load to the {path, error} entries of LoadExtensionsResult.
func extensionLoadErrors(errs []error) []ExtensionLoadError {
	var entries []ExtensionLoadError
	for _, err := range errs {
		if loadErr, ok := errors.AsType[*subprocess.ExtensionLoadError](err); ok && loadErr.Path != "" {
			entries = append(entries, ExtensionLoadError{Path: loadErr.Path, Error: loadErr.Err.Error()})
			continue
		}
		entries = append(entries, ExtensionLoadError{Error: err.Error()})
	}
	return entries
}

func enabledExtensionPaths(resources []icodingagent.ResolvedResource) []string {
	var paths []string
	for _, resource := range resources {
		if resource.Enabled {
			paths = append(paths, resource.Path)
		}
	}
	return paths
}

func (l *DefaultResourceLoader) closeExtensionFiles() {
	l.files.Close()
	l.files, l.filesConfigs = LoadExtensionsResult{}, nil
}

// Close shuts down the Extension Host the default extension load started. The loaded extensions stop.
func (l *DefaultResourceLoader) Close() {
	l.opMu.Lock()
	defer l.opMu.Unlock()
	l.closeExtensionFiles()
}
