// Package compiledin is the registry of the compiled-in extensions an extension names by reference (createCodemodeExtension, createToolSearchExtension): Pi's
// factories return functions, which cannot cross the process boundary, so the SDKs send the name and the options and the host builds its own factory of that name
// (coding/session_graph_extension_factories.go).
//
// A built-in registers its factory from the package that implements it (coding/extension/builtin), not from the session code that looks it up: a program that
// imports the coding library without the built-ins links neither the sandbox's wasm runtime nor the tool-search index, and a reference to a built-in it does not
// link fails as in a build that strips it.
package compiledin

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Factory builds one compiled-in extension from the JSON options the extension passed (empty or null when it passed none). It decodes them into a struct of its own.
type Factory func(options json.RawMessage) (extension.InlineExtension, error)

// HasOptions reports whether the extension passed options.
func HasOptions(options json.RawMessage) bool {
	return len(options) != 0 && string(options) != "null"
}

// OptionsError is the failure of the extension name to read its options.
func OptionsError(name string, err error) error {
	return fmt.Errorf("options of the %s extension: %w", name, err)
}

// factories holds the registered factories, two at most, so a list is searched; a map would allocate its table at the first Register.
var factories []registered

type registered struct {
	name    string
	factory Factory
}

// Register records the factory of the compiled-in extension name, replacing an earlier one of that name.
func Register(name string, factory Factory) {
	for i := range factories {
		if factories[i].name == name {
			factories[i].factory = factory
			return
		}
	}
	if factories == nil {
		factories = make([]registered, 0, 2)
	}
	factories = append(factories, registered{name, factory})
}

// Lookup returns the factory of the compiled-in extension name.
func Lookup(name string) (Factory, bool) {
	for _, r := range factories {
		if r.name == name {
			return r.factory, true
		}
	}
	return nil, false
}
