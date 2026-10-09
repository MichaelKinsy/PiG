// Ports the extension half of packages/coding-agent/src/core/resource-loader.ts (DefaultResourceLoader.reload, loadProjectTrustExtensions,
// loadCurrentExtensionSet, loadFinalExtensionSet, loadExtensionPaths, loadExtensionFactories, omitReplacedExtensions and
// collectExtensionPackageWarnings). The subprocess Extension Host loads file extensions; inline and built-in extensions are the
// in-process factories of builtInExtensions and the loader's inline list.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

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
	Flags Args
	// Inline are the extensionFactories: built-in and inline extensions.
	Inline    []extension.InlineExtension
	Mode      extension.ExtensionMode
	Registry  *codingagent.ModelRegistry
	Resolvers []extsource.ResolveFunc
	// Runtime is the extension runtime the Session's runner binds; the compiled-in factories register against it. Nil: the loader creates one,
	// which the caller reads back with FactoryRuntime.
	Runtime *extension.ExtensionRuntime
	// EventBus is the extension-to-extension event bus the factories share. Nil: the loader creates one.
	EventBus extension.EventBus
}

// FactoryRuntime is the runtime the compiled-in factories register against.
func (l *extensionSetLoader) FactoryRuntime() *extension.ExtensionRuntime {
	if l.Runtime == nil {
		l.Runtime = extension.CreateExtensionRuntime()
	}
	return l.Runtime
}

func (l *extensionSetLoader) eventBus() extension.EventBus {
	if l.EventBus == nil {
		l.EventBus = extension.CreateEventBus()
	}
	return l.EventBus
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
	// resource-loader.ts:376-377 keeps the inputs that are not built-in, and a bare factory is named by its 1-based place among them (:1138-1141).
	index := 0
	for _, input := range l.Inline {
		named, isNamed := input.(extension.NamedInlineExtension)
		if isNamed && named.Builtin {
			continue
		}
		index++
		factory, name := extension.ExtensionFactory(nil), strconv.Itoa(index)
		if isNamed {
			factory, name = named.Factory, named.Name
		} else {
			factory = input.(extension.ExtensionFactory)
		}
		path := "<inline:" + name + ">"
		ext, err := l.runExtensionFactory(factory, name, path, codingagent.CreateSyntheticSourceInfo(path, codingagent.SyntheticSourceInfoOptions{Source: codingagent.SyntheticPathSource(path)}))
		if err != nil {
			errs = append(errs, extensionSetError{Path: path, Error: err.Error()})
			continue
		}
		ext.Hidden = isNamed && named.Hidden
		ext.Replaceable = isNamed && named.Replaceable
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
		index := slices.IndexFunc(l.Inline, func(input extension.InlineExtension) bool {
			named, ok := builtinExtension(input)
			return ok && codingagent.BuiltinPathPrefix+named.Name == path
		})
		if index < 0 {
			errs = append(errs, extensionSetError{Path: path, Error: "Unknown built-in extension: " + path})
			continue
		}
		builtin, _ := builtinExtension(l.Inline[index])
		ext, err := l.runExtensionFactory(builtin.Factory, builtin.Name, path, codingagent.CreateSyntheticSourceInfo(path, codingagent.SyntheticSourceInfoOptions{Source: codingagent.SyntheticPathSource(path)}))
		if err != nil {
			errs = append(errs, extensionSetError{Path: path, Error: err.Error()})
			continue
		}
		ext.Hidden = true
		ext.Replaceable = builtin.Replaceable
		extensions = append(extensions, ext)
	}
	return extensions, errs
}

// runExtensionFactory runs an inline extension's factory through the factory loader against the loader's runtime and names the
// extension by path, giving its commands and tools the extension's source.
// Ports .upstream/v1.1.0/packages/coding-agent/src/core/resource-loader.ts:1131-1153 (loadExtensionFactories) and :906-918 (applyExtensionSourceInfo).
func (l *extensionSetLoader) runExtensionFactory(factory extension.ExtensionFactory, name, path string, info codingagent.PiSourceInfo) (extension.Extension, error) {
	ext, err := factoryload.LoadExtensionFromFactory(factory, l.CWD, l.eventBus(), l.FactoryRuntime(), path, factoryload.WithSourceInfo(info))
	if err != nil {
		return extension.Extension{}, err
	}
	ext.Name, ext.Path, ext.ResolvedPath = name, path, path
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
// It reads each package root once, in first-seen order, and fails when a manifest cannot be parsed. Only an extension resolved from a
// package directory has a package root (PathMetadata.packageRoot); a project or user extension and a local file source do not.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:66-93 (collectExtensionPackageWarnings).
func extensionPackageWarnings(configs []subprocess.ExtConfig) ([]extensionSetWarning, error) {
	var roots []string
	for _, config := range configs {
		if config.PackageRoot != "" && !slices.Contains(roots, config.PackageRoot) {
			roots = append(roots, config.PackageRoot)
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
		// resource-loader.ts omitReplacedExtensions reads `extension.tools.keys()`, the live registry: a factory-loaded extension keeps its tools
		// there, not in ToolOrder.
		for _, name := range ext.RegisteredToolNames() {
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
			// pig divergence (D2): the remedy names PiG's own command; Pi's resource-loader.ts omitReplacedExtensions says `pi config`.
			*warnings = append(*warnings, extensionSetWarning{
				Path:    ext.Path,
				Warning: fmt.Sprintf("Extension %s registers %s `%s`, so built-in extension `%s` was not loaded. To use `%s`, run `%s config` and make sure it is enabled under Built-in extensions, then disable or remove the existing extension. We recommend only having one or the other loaded at a time.", replacement.Path, kind, registered, builtin, builtin, codingagent.AppName),
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
func builtInExtensionNames(extensions []extension.InlineExtension) []string {
	var names []string
	for _, input := range extensions {
		if builtin, ok := builtinExtension(input); ok {
			names = append(names, builtin.Name)
		}
	}
	return names
}

// hasInlineExtensions reports whether extensions holds an inline extension that is not built-in, which loadExtensionFactories loads.
func hasInlineExtensions(extensions []extension.InlineExtension) bool {
	return slices.ContainsFunc(extensions, func(input extension.InlineExtension) bool {
		_, isBuiltin := builtinExtension(input)
		return !isBuiltin
	})
}

// builtinExtension is resource-loader.ts:111 isBuiltinExtension: an input that is not a bare factory and sets `builtin`.
func builtinExtension(input extension.InlineExtension) (extension.NamedInlineExtension, bool) {
	named, ok := input.(extension.NamedInlineExtension)
	return named, ok && named.Builtin
}

// cliBuiltinExtensionNames are the names of the CLI's built-in extensions in extensions/index.ts order. `pig config` lists each as
// a `builtin:<name>` resource. pig additive (D92): a stripped built-in is left out.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14 with package-manager-cli.ts:842-844.
func cliBuiltinExtensionNames() []string {
	return configBuiltinExtensionNames(builtInExtensions)
}

// configBuiltinExtensionNames are the names of the built-in entries of extensions, without the stripped ones (D92).
func configBuiltinExtensionNames(extensions []extension.InlineExtension) []string {
	return slices.DeleteFunc(builtInExtensionNames(extensions), func(name string) bool {
		return pigstrip.Has(pigstrip.ListExtensions, name)
	})
}

// enabledBuiltinExtensionPaths lists the `builtin:<name>` paths a reload loads: the `-e builtin:<name>` paths first, then, unless
// --no-extensions, the built-in extensions the settings leave enabled, in precedence order. A stripped built-in is left out
// silently, the explicit `-e` path included.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:552-573 with package-manager.ts:970-986 and :996-1006.
func enabledBuiltinExtensionPaths(sm *codingagent.SettingsManager, flags Args, names []string) []string {
	explicit, _ := packagemanager.ResolveBuiltinExtensionSources(flags.Extensions, "temporary")
	var paths []string
	add := func(path string) {
		if !slices.Contains(paths, path) && !builtinExtensionStripped(path) {
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
	// --no-mcp is main.ts's `disabledBuiltinExtensions: parsed.noMcp ? ["mcp"] : undefined`: the loader drops those built-in
	// paths even when the settings or `-e` enable them (resource-loader.ts: the filter over extensionPaths).
	if flags.NoMcp {
		paths = slices.DeleteFunc(paths, func(path string) bool { return path == codingagent.BuiltinPathPrefix+"mcp" })
	}
	return paths
}

// builtinExtensionStripped reports whether the built-in extension at path is stripped: a Piglet Binary compiled it out (its OFF
// shim records it in pigstrip) or the active Piglet's strip list names it (applyPigletStrip records it in pigstrip). Like
// upstream's disabledBuiltinExtensions filter in resource-loader.ts (`--no-mcp`), a stripped built-in path is dropped before
// loading, so it neither loads nor reports `Unknown built-in extension`; a name that was never a built-in still reaches the
// loader and fails with that message.
// pig additive (D92): stripped built-in extensions are filtered out of the built-in extension paths silently.
func builtinExtensionStripped(path string) bool {
	name, ok := strings.CutPrefix(path, codingagent.BuiltinPathPrefix)
	return ok && pigstrip.Has(pigstrip.ListExtensions, name)
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
