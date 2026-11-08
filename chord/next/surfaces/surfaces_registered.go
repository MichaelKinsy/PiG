package surfaces

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/surfaces/registered.ts. Plugin scripts that register themselves (slice 11 §1, §2, decision "Slice
// 11's points" (P2)): a facet's built script served as `chordFacet("<plugin>", "<digest>", function (require, module,
// exports) { … })`, loaded with `<script src>` (a page under a `'self'` CSP: no `eval`, no `new Function`) or run by the
// terminal's runtime; the client evaluates the registered function with a `require` that answers only its host packages.
//
// A Go client's script is the Go function a plugin built into the client registers (RegisteredScript.Run): what Pi's client
// holds as the function a JavaScript script passed to `chordFacet`. The script text itself (RegisteredSource) is what the
// server serves to JavaScript clients. Pi's `interop` (an ES module namespace wrapped for a CommonJS import, with
// `__esModule`) has no Go form: a module is the value ClientModules holds, as it is (D550).

// Register is the global function a registered script calls: defined by the client before any plugin script runs.
const Register = "chordFacet"

// Module is the `module` a registered function fills: its exports.
type Module struct {
	Exports ModuleExports
}

// ModuleExports are a module's exports: Default is the facet a script exports.
type ModuleExports struct {
	Default any
}

// FacetFunction is a built facet's function, as its script registers it. Its require answers the client's host packages; its
// error is what the script throws.
type FacetFunction func(require func(specifier string) (any, error), module *Module) error

// RegisteredScript is one registered script: its plugin, its digest and its function.
type RegisteredScript struct {
	Plugin string
	Digest string
	Run    FacetFunction
}

// ClientModules are the modules a client's plugin scripts import, by specifier (the app's API modules for the kind, ...).
type ClientModules map[string]any

// RegisteredSource is the text of a registered script for plugin's facet built as source (one function expression, the
// build's wrapped CommonJS script) with digest. Its source map comment stays on its own line, before the closing call.
func RegisteredSource(plugin, digest, source string) string {
	text := append([]byte(Register+"("), jsstring.AppendString(nil, plugin)...)
	text = append(text, ',')
	text = jsstring.AppendString(text, digest)
	return string(text) + "," + source + "\n);\n"
}

// EvaluateRegistered runs script's function and returns its facet (its default export). Its require answers modules only (the
// build kept nothing else external for a client kind); anything else fails naming the plugin. It returns what the script
// throws as its error (a panic, and a missing function, are recovered as one: client.ts `entryOf.prepare` catches what
// evaluateRegistered throws), and an error when its default export is no facet.
func EvaluateRegistered(script RegisteredScript, modules ClientModules) (host.Facet, error) {
	where := fmt.Sprintf("plugin %q", script.Plugin)
	var module Module
	err := runScript(script, func(specifier string) (any, error) {
		if value, ok := modules[specifier]; ok {
			return value, nil
		}
		return nil, fmt.Errorf("%s: %q is not one of this client's host packages", where, specifier)
	}, &module)
	if err != nil {
		return host.Facet{}, err
	}
	// A trust boundary: plugin code; its default export is checked to be a facet.
	if facet, ok := module.Exports.Default.(host.Facet); ok && facet != (host.Facet{}) {
		return facet, nil
	}
	return host.Facet{}, fmt.Errorf("%s: its default export is not a facet (defineFacet)", where)
}

// runScript runs script's function under a recover: what a plugin's script throws, a Go panic or a call of a nil function
// among it, is its error, as the TypeError or the throw of JavaScript that Pi's client catches. A trust boundary: plugin code.
func runScript(script RegisteredScript, require func(specifier string) (any, error), module *Module) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
			} else {
				err = fmt.Errorf("%v", recovered)
			}
		}
	}()
	return script.Run(require, module)
}
