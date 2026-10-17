// Ports the extension pieces of packages/coding-agent/src/core/resource-loader.ts that cmd/pig's build sequences (loadExtensionPaths'
// built-in paths, loadExtensionFactories, omitReplacedExtensions and collectExtensionPackageWarnings) over coding/extensionset. The
// subprocess Extension Host loads file extensions; inline and built-in extensions are the in-process factories of builtInExtensions and
// the loader's inline list. The reload sequencing (the pre-trust pass, the trust callback and the final pass) runs in cli_runtime_build.go
// and package_command_trust.go.

package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extensionset"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// extensionSetError and extensionSetWarning are the {path, error} and {path, warning} entries of types.ts LoadExtensionsResult.
type extensionSetError struct{ Path, Error string }

type extensionSetWarning struct{ Path, Warning string }

// extensionSetLoader selects and loads the extensions of one cwd, as DefaultResourceLoader does.
type extensionSetLoader struct {
	CWD      string
	Settings *codingagent.SettingsManager
	// Flags supplies --extension paths (additionalExtensionPaths) and --no-extensions.
	Flags Args
	// Inline are the extensionFactories: built-in and inline extensions.
	Inline []extension.InlineExtension
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

// loadBuiltinsAfter runs the enabled built-in extensions and leaves out the replaceable ones that another extension in loaded
// replaces. It returns the built-in extensions that remain, their load errors and the replacement warnings.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:703-735,772-778.
func (l *extensionSetLoader) loadBuiltinsAfter(loaded []extension.Extension) (builtins []extension.Extension, errs []extensionSetError, warnings []extensionSetWarning) {
	exts, errs := l.loadBuiltinExtensions(enabledBuiltinExtensionPaths(l.Settings, l.Flags, extensionset.BuiltinNames(l.Inline)))
	for _, ext := range omitReplacedExtensions(slices.Concat(loaded, exts), &warnings) {
		if strings.HasPrefix(ext.Path, codingagent.BuiltinPathPrefix) {
			builtins = append(builtins, ext)
		}
	}
	return builtins, errs, warnings
}

// extensionPackageWarnings warns about an extension package whose package.json lists host-provided packages under `dependencies`
// (resource-loader.ts collectExtensionPackageWarnings). Only an extension resolved from a package directory has a package root.
func extensionPackageWarnings(configs []subprocess.ExtConfig) ([]extensionSetWarning, error) {
	var roots []string
	for _, config := range configs {
		if config.PackageRoot != "" {
			roots = append(roots, config.PackageRoot)
		}
	}
	warnings, err := codingagent.ExtensionPackageWarnings(roots)
	if err != nil || len(warnings) == 0 {
		return nil, err
	}
	return fromCodingAgentWarnings(warnings), nil
}

func fromCodingAgentWarnings(warnings []codingagent.ExtensionWarning) []extensionSetWarning {
	out := make([]extensionSetWarning, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, extensionSetWarning{Path: warning.Path, Warning: warning.Warning})
	}
	return out
}

// mergeExtensionWarnings keeps one warning per path: the last one, at the position of the first (resource-loader.ts mergeExtensionWarnings).
func mergeExtensionWarnings(existing, added []extensionSetWarning) []extensionSetWarning {
	toCodingAgent := func(warnings []extensionSetWarning) []codingagent.ExtensionWarning {
		var out []codingagent.ExtensionWarning
		for _, warning := range warnings {
			out = append(out, codingagent.ExtensionWarning{Path: warning.Path, Warning: warning.Warning})
		}
		return out
	}
	return fromCodingAgentWarnings(codingagent.MergeExtensionWarnings(toCodingAgent(existing), toCodingAgent(added)))
}

// builtInExtensions are the built-in extensions of the CLI (extensions/index.ts builtInExtensions). Each Builtin entry supplies the
// code of its `builtin:<name>` path.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14.
var builtInExtensions = nativeBuiltInExtensions(nil)

// cliBuiltinExtensionNames are the names of the CLI's built-in extensions in extensions/index.ts order. `pig config` lists each as
// a `builtin:<name>` resource. pig additive (D92): a stripped built-in is left out.
// Ports .upstream/v0.99.2/packages/coding-agent/src/extensions/index.ts:7-14 with package-manager-cli.ts:842-844.
func cliBuiltinExtensionNames() []string {
	return configBuiltinExtensionNames(builtInExtensions)
}

// configBuiltinExtensionNames are the names of the built-in entries of extensions, without the stripped ones (D92).
func configBuiltinExtensionNames(extensions []extension.InlineExtension) []string {
	return slices.DeleteFunc(extensionset.BuiltinNames(extensions), func(name string) bool {
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
		// validateConfiguredResourceEntries has already reported a pattern error.
		items, _ := packagemanager.ResolveBuiltinExtensions(sm, names)
		for _, item := range items {
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

// factories is the shared in-process factory loader over the loader's cwd, inline list, runtime and event bus.
func (l *extensionSetLoader) factories() *extensionset.Loader {
	return &extensionset.Loader{CWD: l.CWD, Inline: l.Inline, Runtime: l.FactoryRuntime(), EventBus: l.eventBus()}
}

func fromLoadErrors(errs []extensionset.LoadError) []extensionSetError {
	var out []extensionSetError
	for _, err := range errs {
		out = append(out, extensionSetError{Path: err.Path, Error: err.Error})
	}
	return out
}

// loadInlineExtensions runs the factories of the extensions that are not built-in (extensionset.Loader.LoadInline).
func (l *extensionSetLoader) loadInlineExtensions() ([]extension.Extension, []extensionSetError) {
	extensions, errs := l.factories().LoadInline()
	return extensions, fromLoadErrors(errs)
}

// loadBuiltinExtensions runs the factory of each `builtin:<name>` path (extensionset.Loader.LoadBuiltin).
func (l *extensionSetLoader) loadBuiltinExtensions(paths []string) ([]extension.Extension, []extensionSetError) {
	extensions, errs := l.factories().LoadBuiltin(paths)
	return extensions, fromLoadErrors(errs)
}

// omitReplacedExtensions leaves out the replaceable extensions another extension replaces (extensionset.OmitReplaced); a nil warnings drops the warnings.
func omitReplacedExtensions(extensions []extension.Extension, warnings *[]extensionSetWarning) []extension.Extension {
	if warnings == nil {
		return extensionset.OmitReplaced(extensions, nil)
	}
	var added []codingagent.ExtensionWarning
	kept := extensionset.OmitReplaced(extensions, &added)
	for _, warning := range added {
		*warnings = append(*warnings, extensionSetWarning{Path: warning.Path, Warning: warning.Warning})
	}
	return kept
}

// explicitBuiltinPaths are the `builtin:<name>` paths of the -e arguments, which load before the file extensions (resource-loader.ts:566-575).
func explicitBuiltinPaths(flags Args) []string {
	explicit, _ := packagemanager.ResolveBuiltinExtensionSources(flags.Extensions, "temporary")
	var paths []string
	for _, item := range explicit {
		if !builtinExtensionStripped(item.Path) {
			paths = append(paths, item.Path)
		}
	}
	return paths
}
