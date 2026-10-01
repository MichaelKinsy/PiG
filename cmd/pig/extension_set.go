// Ports the extension half of packages/coding-agent/src/core/resource-loader.ts (DefaultResourceLoader.reload, loadProjectTrustExtensions,
// loadCurrentExtensionSet, loadFinalExtensionSet, loadExtensionPaths, loadExtensionFactories, omitReplacedExtensions and
// collectExtensionPackageWarnings). The subprocess Extension Host loads file extensions; inline and built-in extensions are the
// in-process factories of builtInExtensions and the loader's inline list.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// inlineExtension is types.ts InlineExtension: an extension whose code is a Go factory instead of a file. Path is `<inline:name>`,
// or `builtin:name` for a built-in extension, which loads as the `builtin:<name>` extension resource.
type inlineExtension struct {
	Name    string
	Factory func() (extension.Extension, error)
	// Hidden omits the extension from the startup Extensions list.
	Hidden bool
	// Replaceable leaves the extension out when another extension registers a tool, command or flag with a name it registers.
	Replaceable bool
	// Builtin supplies the code of the `builtin:<name>` extension resource: it loads by default, `pig config` lists it,
	// `-builtin:<name>` in the `extensions` setting and --no-extensions disable it, and `-e builtin:<name>` loads it explicitly.
	Builtin bool
}

// extensionSetError and extensionSetWarning are the {path, error} and {path, warning} entries of types.ts LoadExtensionsResult.
type extensionSetError struct{ Path, Error string }

type extensionSetWarning struct{ Path, Warning string }

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

// extensionSetLoader selects and loads the extensions of one cwd, as DefaultResourceLoader does.
type extensionSetLoader struct {
	CWD      string
	AgentDir string
	Settings *codingagent.SettingsManager
	// Flags supplies --extension paths (additionalExtensionPaths) and --no-extensions.
	Flags CLIFlags
	// Inline are the extensionFactories: built-in and inline extensions.
	Inline    []inlineExtension
	Mode      extension.ExtensionMode
	Registry  *codingagent.ModelRegistry
	Resolvers []extsource.ResolveFunc
}

// Reload rediscovers the extension set. With resolveProjectTrust it first loads the pre-trust set with the project untrusted and
// hands it to the callback; the final set then loads for the trust the callback returns (resource-loader.ts reload). The caller
// closes the result.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:531-581 (reload) and :585-601 (loadProjectTrustExtensions).
func (l *extensionSetLoader) Reload(ctx context.Context, resolveProjectTrust func(pre extensionSetResult) (bool, error)) (*extensionSetResult, error) {
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
func (l *extensionSetLoader) configs(preTrust bool) []subprocess.ExtConfig {
	var scopes *[]string
	if preTrust {
		scopes = &[]string{"user"}
	}
	return collectExtensionConfigs(l.CWD, l.AgentDir, l.Settings, l.Flags, scopes, l.Resolvers...)
}

// loadFiles loads the file extensions with the subprocess Host that startup owns.
func (l *extensionSetLoader) loadFiles(ctx context.Context, startup *startupExtensionSet, configs []subprocess.ExtConfig) ([]extension.Extension, []extensionSetError) {
	loaded, _, _, errs := loadFinalSubprocessExtensions(ctx, l.CWD, l.Mode, l.Registry, configs, nil, nil, startup)
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
func (l *extensionSetLoader) loadCurrentExtensionSet(ctx context.Context, startup *startupExtensionSet, includeInline bool) (*extensionSetResult, error) {
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
func (l *extensionSetLoader) loadFinalExtensionSet(ctx context.Context, startup *startupExtensionSet, pre *extensionSetResult) (*extensionSetResult, error) {
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
	builtinPaths := enabledBuiltinExtensionPaths(l.Settings, l.Flags, builtInExtensionNames(l.Inline))
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

// loadBuiltinsAfter runs the enabled built-in extensions and leaves out the replaceable ones that another extension in loaded
// replaces. It returns the built-in extensions that remain, their load errors and the replacement warnings.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:703-735,772-778.
func (l *extensionSetLoader) loadBuiltinsAfter(loaded []extension.Extension) (builtins []extension.Extension, errs []extensionSetError, warnings []extensionSetWarning) {
	exts, errs := l.loadBuiltinExtensions(enabledBuiltinExtensionPaths(l.Settings, l.Flags, builtInExtensionNames(l.Inline)))
	for _, ext := range omitReplacedExtensions(slices.Concat(loaded, exts), &warnings) {
		if strings.HasPrefix(ext.Path, codingagent.BuiltinPathPrefix) {
			builtins = append(builtins, ext)
		}
	}
	return builtins, errs, warnings
}

// loadInlineExtensions runs the factories of the extensions that are not built-in, naming each `<inline:name>`.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:1122-1145 (loadExtensionFactories).
func (l *extensionSetLoader) loadInlineExtensions() ([]extension.Extension, []extensionSetError) {
	var extensions []extension.Extension
	var errs []extensionSetError
	for _, input := range l.Inline {
		if input.Builtin {
			continue
		}
		path := "<inline:" + input.Name + ">"
		ext, err := runExtensionFactory(input, path, codingagent.PiSourceInfo{Path: path, Source: codingagent.SyntheticPathSource(path), Scope: "temporary", Origin: "top-level"})
		if err != nil {
			errs = append(errs, extensionSetError{Path: path, Error: err.Error()})
			continue
		}
		ext.Hidden = input.Hidden
		ext.Replaceable = input.Replaceable
		extensions = append(extensions, ext)
	}
	return extensions, errs
}

// loadBuiltinExtensions runs the factory of each `builtin:<name>` path. An unknown name is a load error.
// A built-in extension's source info is the synthetic one of its path, scope "temporary", whichever settings scope enabled it:
// applyExtensionSourceInfo resolves a synthetic path with getDefaultSourceInfoForPath (resource-loader.ts:906-975).
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:706-735 (loadExtensionPaths).
func (l *extensionSetLoader) loadBuiltinExtensions(paths []string) ([]extension.Extension, []extensionSetError) {
	var extensions []extension.Extension
	var errs []extensionSetError
	for _, path := range paths {
		index := slices.IndexFunc(l.Inline, func(input inlineExtension) bool {
			return input.Builtin && codingagent.BuiltinPathPrefix+input.Name == path
		})
		if index < 0 {
			errs = append(errs, extensionSetError{Path: path, Error: "Unknown built-in extension: " + path})
			continue
		}
		ext, err := runExtensionFactory(l.Inline[index], path, codingagent.PiSourceInfo{Path: path, Source: codingagent.SyntheticPathSource(path), Scope: "temporary", Origin: "top-level"})
		if err != nil {
			errs = append(errs, extensionSetError{Path: path, Error: err.Error()})
			continue
		}
		ext.Hidden = true
		ext.Replaceable = l.Inline[index].Replaceable
		extensions = append(extensions, ext)
	}
	return extensions, errs
}

// runExtensionFactory runs an inline extension's factory and names the extension by path, giving its commands and tools the
// extension's source.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:906-918 (applyExtensionSourceInfo).
func runExtensionFactory(input inlineExtension, path string, info codingagent.PiSourceInfo) (extension.Extension, error) {
	ext, err := input.Factory()
	if err != nil {
		return extension.Extension{}, err
	}
	ext.Name, ext.Path, ext.ResolvedPath, ext.SourceInfo = input.Name, path, path, info
	for name, command := range ext.Commands {
		command.SourceInfo = info
		ext.Commands[name] = command
	}
	for name, tool := range ext.Tools {
		tool.SourceInfo = info
		ext.Tools[name] = tool
	}
	return ext, nil
}

// hostProvidedExtensionPackages are the packages an extension package must declare in peerDependencies.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:53-64.
var hostProvidedExtensionPackages = []string{
	"@earendil-works/pi-agent-core", "@earendil-works/pi-ai", "@earendil-works/pi-coding-agent", "@earendil-works/pi-tui",
	"@mariozechner/pi-agent-core", "@mariozechner/pi-ai", "@mariozechner/pi-coding-agent", "@mariozechner/pi-tui",
	"@sinclair/typebox", "typebox",
}

// extensionPackageWarnings warns about an extension package whose package.json lists host-provided packages under `dependencies`.
// It reads each package root once, in first-seen order, and fails when a manifest cannot be parsed. Only Package resources have a
// package root; a project or user extension does not.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:66-93 (collectExtensionPackageWarnings).
func extensionPackageWarnings(configs []subprocess.ExtConfig) ([]extensionSetWarning, error) {
	var roots []string
	for _, config := range configs {
		info := codingagent.PiSourceInfoValue(config.SourceInfo)
		if info.Origin == "package" && info.BaseDir != "" && !slices.Contains(roots, info.BaseDir) {
			roots = append(roots, info.BaseDir)
		}
	}
	var warnings []extensionSetWarning
	for _, root := range roots {
		manifestPath := filepath.Join(root, "package.json")
		data, err := os.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var manifest any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "\ufeff")), &manifest); err != nil {
			return nil, err
		}
		if manifest == nil {
			return nil, errors.New("Cannot read properties of null (reading 'dependencies')")
		}
		object, _ := manifest.(map[string]any)
		dependencies, _ := object["dependencies"].(map[string]any)
		var host []string
		for name := range dependencies {
			if slices.Contains(hostProvidedExtensionPackages, name) {
				host = append(host, name)
			}
		}
		if len(host) == 0 {
			continue
		}
		slices.Sort(host)
		warnings = append(warnings, extensionSetWarning{
			Path:    manifestPath,
			Warning: fmt.Sprintf(`Host-provided extension packages must be declared in peerDependencies with a "*" range, not dependencies: %s. Installed copies can bypass the extension loader and create duplicate runtime modules.`, strings.Join(host, ", ")),
		})
	}
	return warnings, nil
}

// mergeExtensionWarnings keeps one warning per path: the last one, at the position of the first.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:95-102 (mergeExtensionWarnings).
func mergeExtensionWarnings(existing, added []extensionSetWarning) []extensionSetWarning {
	merged := make([]extensionSetWarning, 0, len(existing)+len(added))
	for _, warning := range slices.Concat(existing, added) {
		if index := slices.IndexFunc(merged, func(seen extensionSetWarning) bool { return seen.Path == warning.Path }); index >= 0 {
			merged[index] = warning
			continue
		}
		merged = append(merged, warning)
	}
	return merged
}

// omitReplacedExtensions leaves out the replaceable extensions that share a tool, command or flag name with another extension.
// A replaced built-in extension adds a warning to warnings.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:114-157 (omitReplacedExtensions).
func omitReplacedExtensions(extensions []extension.Extension, warnings *[]extensionSetWarning) []extension.Extension {
	names := func(ext extension.Extension) []string {
		var out []string
		for _, name := range ext.ToolOrder {
			out = append(out, "tool:"+name)
		}
		for _, name := range ext.CommandOrder {
			out = append(out, "command:"+name)
		}
		for _, name := range ext.FlagOrder {
			out = append(out, "flag:"+name)
		}
		return out
	}
	// A Map keeps the later entry of a repeated name.
	taken := make(map[string]extension.Extension)
	for _, ext := range extensions {
		if ext.Replaceable {
			continue
		}
		for _, name := range names(ext) {
			taken[name] = ext
		}
	}
	kept := make([]extension.Extension, 0, len(extensions))
	for _, ext := range extensions {
		if !ext.Replaceable {
			kept = append(kept, ext)
			continue
		}
		var replacement extension.Extension
		var replacedName string
		for _, name := range names(ext) {
			if owner, found := taken[name]; found {
				replacement, replacedName = owner, name
				break
			}
		}
		if replacedName == "" {
			kept = append(kept, ext)
			continue
		}
		if builtin, isBuiltin := strings.CutPrefix(ext.Path, codingagent.BuiltinPathPrefix); isBuiltin && warnings != nil {
			// name.split(":", 2) keeps the first two parts: a name that contains a colon is cut at it.
			parts := strings.SplitN(replacedName, ":", 3)
			kind, rawName := parts[0], parts[1]
			registered := rawName
			switch kind {
			case "command":
				registered = "/" + rawName
			case "flag":
				registered = "--" + rawName
			}
			*warnings = append(*warnings, extensionSetWarning{
				Path:    ext.Path,
				Warning: fmt.Sprintf("Extension %s registers %s `%s`, so built-in extension `%s` was not loaded. To use `%s`, run `pi config` and make sure it is enabled under Built-in extensions, then disable or remove the existing extension. We recommend only having one or the other loaded at a time.", replacement.Path, kind, registered, builtin, builtin),
			})
		}
	}
	return kept
}

// builtInExtensions are the built-in extensions of the CLI (extensions/index.ts builtInExtensions). Each Builtin entry supplies the
// code of its `builtin:<name>` path.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14.
var builtInExtensions = nativeBuiltInExtensions(nil)

// builtInExtensionNames are the names of the entries of extensions that supply `builtin:<name>` code, in order.
// Ports .upstream/v0.99.1/packages/coding-agent/src/package-manager-cli.ts:842-844.
func builtInExtensionNames(extensions []inlineExtension) []string {
	var names []string
	for _, input := range extensions {
		if input.Builtin {
			names = append(names, input.Name)
		}
	}
	return names
}

// cliBuiltinExtensionNames are the names of the CLI's built-in extensions in extensions/index.ts order. `pig config` lists each as
// a `builtin:<name>` resource.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14 with package-manager-cli.ts:842-844.
func cliBuiltinExtensionNames() []string {
	return builtInExtensionNames(builtInExtensions)
}

// enabledBuiltinExtensionPaths lists the `builtin:<name>` paths a reload loads: the `-e builtin:<name>` paths first, then, unless
// --no-extensions, the built-in extensions the settings leave enabled, in precedence order.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:552-573 with package-manager.ts:970-986 and :996-1006.
func enabledBuiltinExtensionPaths(sm *codingagent.SettingsManager, flags CLIFlags, names []string) []string {
	explicit, _ := packagemanager.ResolveBuiltinExtensionSources(flags.Extensions, "temporary")
	var paths []string
	add := func(path string) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	for _, item := range explicit {
		add(item.Path)
	}
	if !flags.NoExtensions {
		for _, item := range packagemanager.ResolveBuiltinExtensions(sm, names) {
			if item.Enabled {
				add(item.Path)
			}
		}
	}
	return paths
}

// builtinLlamaEnabled reports whether the built-in llama.cpp extension loads: it is enabled by default, `-builtin:llama.cpp` in the
// `extensions` setting and --no-extensions disable it, and `-e builtin:llama.cpp` loads it explicitly.
// Ports .upstream/v0.99.1/packages/coding-agent/src/extensions/index.ts:8 with resource-loader.ts:552-573.
func builtinLlamaEnabled(sm *codingagent.SettingsManager, flags CLIFlags) bool {
	return slices.Contains(enabledBuiltinExtensionPaths(sm, flags, []string{llamaBuiltinName}), codingagent.BuiltinPathPrefix+llamaBuiltinName)
}

// extensionErrorDiagnostics formats extension set load errors as startup diagnostics. A built-in extension's error is the bare
// message (`Unknown built-in extension: <path>` or the factory's error, resource-loader.ts:717-733), with no loader prefix.
// Ports .upstream/v0.99.1/packages/coding-agent/src/main.ts:793-796.
func extensionErrorDiagnostics(errs []extensionSetError) []codingagent.AgentSessionRuntimeDiagnostic {
	diagnostics := make([]codingagent.AgentSessionRuntimeDiagnostic, 0, len(errs))
	for _, loadErr := range errs {
		diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf(`Failed to load extension "%s": %s`, loadErr.Path, loadErr.Error)})
	}
	return diagnostics
}

// extensionWarningDiagnostics formats extension package warnings as startup diagnostics.
// Ports .upstream/v0.99.1/packages/coding-agent/src/main.ts:797-800.
func extensionWarningDiagnostics(warnings []extensionSetWarning) []codingagent.AgentSessionRuntimeDiagnostic {
	diagnostics := make([]codingagent.AgentSessionRuntimeDiagnostic, 0, len(warnings))
	for _, warning := range warnings {
		diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "warning", Message: fmt.Sprintf(`Extension package "%s": %s`, warning.Path, warning.Warning)})
	}
	return diagnostics
}
