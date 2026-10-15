// Package extensionset holds the in-process half of the extension set DefaultResourceLoader loads: the inline and built-in extension factories
// (resource-loader.ts loadExtensionFactories, loadExtensionPaths' `builtin:` branch) and omitReplacedExtensions. The CLI's extension set and
// coding.DefaultResourceLoader both call it.
package extensionset

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// LoadError is a {path, error} entry of types.ts LoadExtensionsResult.errors.
type LoadError struct{ Path, Error string }

// Loader runs the extension factories of one cwd.
type Loader struct {
	CWD string
	// Inline are the extensionFactories: built-in and inline extensions.
	Inline []extension.InlineExtension
	// Runtime is the runtime the factories register against. Nil: the loader creates one, which factoryRuntime returns.
	Runtime *extension.ExtensionRuntime
	// EventBus is the bus the factories share. Nil: the loader creates one.
	EventBus extension.EventBus
}

// factoryRuntime is the runtime the compiled-in factories register against.
func (l *Loader) factoryRuntime() *extension.ExtensionRuntime {
	if l.Runtime == nil {
		l.Runtime = extension.CreateExtensionRuntime()
	}
	return l.Runtime
}

func (l *Loader) bus() extension.EventBus {
	if l.EventBus == nil {
		l.EventBus = extension.CreateEventBus()
	}
	return l.EventBus
}

// LoadInline runs the factories of the extensions that are not built-in, naming each `<inline:name>`.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:1122-1145 (loadExtensionFactories).
func (l *Loader) LoadInline() ([]extension.Extension, []LoadError) {
	var extensions []extension.Extension
	var errs []LoadError
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
		ext, err := l.runExtensionFactory(factory, name, path, icodingagent.CreateSyntheticSourceInfo(path, icodingagent.SyntheticSourceInfoOptions{Source: icodingagent.SyntheticPathSource(path)}))
		if err != nil {
			errs = append(errs, LoadError{Path: path, Error: err.Error()})
			continue
		}
		ext.Hidden = isNamed && named.Hidden
		ext.Replaceable = isNamed && named.Replaceable
		extensions = append(extensions, ext)
	}
	return extensions, errs
}

// LoadBuiltin runs the factory of each `builtin:<name>` path. An unknown name is a load error.
// A built-in extension's source info is the synthetic one of its path, scope "temporary", whichever settings scope enabled it:
// applyExtensionSourceInfo resolves a synthetic path with getDefaultSourceInfoForPath (resource-loader.ts:906-975).
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:706-735 (loadExtensionPaths).
func (l *Loader) LoadBuiltin(paths []string) ([]extension.Extension, []LoadError) {
	var extensions []extension.Extension
	var errs []LoadError
	for _, path := range paths {
		// resource-loader.ts:378 builds a Map from the built-in inputs, which keeps the last of two entries with one name.
		var builtin extension.NamedInlineExtension
		found := false
		for _, input := range l.Inline {
			if named, ok := builtinExtension(input); ok && icodingagent.BuiltinPathPrefix+named.Name == path {
				builtin, found = named, true
			}
		}
		if !found {
			errs = append(errs, LoadError{Path: path, Error: "Unknown built-in extension: " + path})
			continue
		}
		ext, err := l.runExtensionFactory(builtin.Factory, builtin.Name, path, icodingagent.CreateSyntheticSourceInfo(path, icodingagent.SyntheticSourceInfoOptions{Source: icodingagent.SyntheticPathSource(path)}))
		if err != nil {
			errs = append(errs, LoadError{Path: path, Error: err.Error()})
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
func (l *Loader) runExtensionFactory(factory extension.ExtensionFactory, name, path string, info icodingagent.PiSourceInfo) (extension.Extension, error) {
	ext, err := factoryload.LoadExtensionFromFactory(factory, l.CWD, l.bus(), l.factoryRuntime(), path, factoryload.WithSourceInfo(info))
	if err != nil {
		return extension.Extension{}, err
	}
	ext.Name, ext.Path, ext.ResolvedPath = name, path, path
	return ext, nil
}

// OmitReplaced leaves out the replaceable extensions that share a tool, command or flag name with another extension.
// A replaced built-in extension adds a warning to warnings.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/resource-loader.ts:114-157 (omitReplacedExtensions).
func OmitReplaced(extensions []extension.Extension, warnings *[]icodingagent.ExtensionWarning) []extension.Extension {
	names := func(ext extension.Extension) []string {
		var out []string
		// resource-loader.ts omitReplacedExtensions reads `extension.tools.keys()`, the live registry: a factory-loaded extension keeps its tools
		// there, not in ToolOrder.
		for _, name := range ext.RegisteredToolNames() {
			out = append(out, "tool:"+name)
		}
		// resource-loader.ts reads `extension.commands.keys()` and `extension.flags.keys()` the same way.
		for _, command := range ext.RegisteredCommands() {
			out = append(out, "command:"+command.Name)
		}
		for _, flag := range ext.RegisteredFlags() {
			out = append(out, "flag:"+flag.Name)
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
		if builtin, isBuiltin := strings.CutPrefix(ext.Path, icodingagent.BuiltinPathPrefix); isBuiltin && warnings != nil {
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
			*warnings = append(*warnings, icodingagent.ExtensionWarning{
				Path:    ext.Path,
				Warning: fmt.Sprintf("Extension %s registers %s `%s`, so built-in extension `%s` was not loaded. To use `%s`, run `%s config` and make sure it is enabled under Built-in extensions, then disable or remove the existing extension. We recommend only having one or the other loaded at a time.", replacement.Path, kind, registered, builtin, builtin, icodingagent.AppName),
			})
		}
	}
	return kept
}

// HasInline reports whether extensions holds an inline extension that is not built-in, which loadExtensionFactories loads.
func HasInline(extensions []extension.InlineExtension) bool {
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

// BuiltinNames are the names of the entries of extensions that supply `builtin:<name>` code, in order.
// Ports .upstream/v1.1.0/packages/coding-agent/src/core/resource-loader.ts:378-383 (the builtinExtensions Map keys).
func BuiltinNames(extensions []extension.InlineExtension) []string {
	var names []string
	for _, input := range extensions {
		// resource-loader.ts:378 keys a Map by name: a repeated name keeps its first place and is listed once (:383 [...keys()]).
		if builtin, ok := builtinExtension(input); ok && !slices.Contains(names, builtin.Name) {
			names = append(names, builtin.Name)
		}
	}
	return names
}
