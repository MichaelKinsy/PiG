// Package factoryload runs the compiled-in extension factories of a binary: Go code that the source of the binary's own main package
// passes to it by value (docs/specs/extension-factory-trust.md). It is the Go port of the in-process half of upstream's extension loader.
//
// Nothing the subprocess side imports may import this package. The factory types live in package extension, beside the API;
// the loader, the registering API and the built-in rows do not.
//
// Ports packages/coding-agent/src/core/extensions/loader.ts (createExtensionAPI, initializeExtension, loadExtensionFromFactory).
package factoryload

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// DefaultExtensionPath is the path of an extension whose factory was given without one.
// upstream: loader.ts:662-668 (loadExtensionFromFactory extensionPath = "<inline>")
const DefaultExtensionPath = "<inline>"

// Option adjusts how LoadExtensionFromFactory builds the extension.
type Option func(*extension.Extension)

// WithSourceInfo gives the extension its source before the factory runs, so every command and tool the factory registers, during
// loading or later, carries it.
// upstream: loader.ts:604-611 (createExtension sets sourceInfo before the factory runs), resource-loader.ts:906-918 (applyExtensionSourceInfo)
func WithSourceInfo(info extension.SourceInfo) Option {
	return func(ext *extension.Extension) { ext.SourceInfo = info }
}

// LoadExtensionFromFactory runs factory against a registering API and returns the extension it registered. The extension's handlers,
// tools and registries are the factory's writes to that API; its runtime changes (provider, MCP server and virtual-model registrations,
// flag defaults) reach runtime only if the factory returns without error. A factory that fails leaves runtime as it was, drops the
// event-bus subscriptions it made, and its API reports `Extension "<path>" failed to load and its API is no longer active.` from then on.
//
// A panic in the factory is a load error: upstream catches every exception a factory throws. The caller sets the extension's name
// and source info. LoadExtensionFromFactory is safe for concurrent calls on one runtime; each call applies only its own changes.
//
// Async contract: upstream awaits the factory (`await factory(load.api)`), so this call blocks until the factory returns.
//
// upstream: loader.ts:613-632 (initializeExtension), :662-671 (loadExtensionFromFactory)
func LoadExtensionFromFactory(factory extension.ExtensionFactory, cwd string, eventBus extension.EventBus, runtime *extension.ExtensionRuntime, extensionPath string, options ...Option) (loaded extension.Extension, err error) {
	if factory == nil {
		return extension.Extension{}, errors.New("extension factory is nil")
	}
	if extensionPath == "" {
		extensionPath = DefaultExtensionPath
	}
	ext := &extension.Extension{
		Path:         extensionPath,
		ResolvedPath: extensionPath,
		Commands:     map[string]extension.RegisteredCommand{},
		Flags:        map[string]extension.ExtensionFlag{},
	}
	for _, option := range options {
		option(ext)
	}
	ext.InitializeEventHandlers()
	ext.InitializeToolRegistry()
	load := newAPI(ext, runtime, cwd, eventBus)
	defer func() {
		// upstream: loader.ts:623-629 catches whatever the factory throws, discards its changes and rethrows it.
		if recovered := recover(); recovered != nil {
			load.discard()
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
			} else {
				err = fmt.Errorf("%v", recovered)
			}
			loaded = extension.Extension{}
		}
	}()
	if err := factory(load); err != nil {
		load.discard()
		return extension.Extension{}, err
	}
	if err := load.commit(); err != nil {
		load.discard()
		return extension.Extension{}, err
	}
	runtime.ReplaceFactoryAPI(extensionPath, load.retire)
	return *ext, nil
}
