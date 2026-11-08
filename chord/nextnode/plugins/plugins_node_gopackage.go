package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Host packages the Go app offers to its plugins (docs/plan/chord-plugins-node-evaluator.md, 10.4). Pi's `HostPackages` are
// module instances of the app's own process, handed to the plugins' `require`; the plugins here run in another process, so
// what crosses is what a call can carry: functions of JSON, and JSON values. Identity and live state do not cross: a
// JavaScript object shared by reference between a plugin and the app, or a function the plugin calls synchronously and that
// returns something that is not JSON, has no Go form, and a plugin that needs one is written against the package's
// asynchronous JSON functions instead (a plugin can reach a live object of the app through a chord type the Go host serves).

// GoExport is one export of a GoPackage: a Go function (GoFunc) or a JSON value (GoValue). The zero value is no export.
type GoExport struct {
	function GoFunction
	value    delta.JsonValue
	isValue  bool
}

// GoFunc is an export that is a Go function. In the plugin it is an asynchronous function `(args, context?) => Promise`: it
// calls fn with the JSON `args` and resolves with fn's JSON result; a Go error rejects it (a *data.ChordError as a
// ChordError, any other as an Error with its message). `context` is the request's chord Context or an AbortSignal: its abort
// rejects the call with the abort's reason at once and cancels fn's context. Calls run concurrently, each on a goroutine of its
// own; fn's context also ends when the process does.
func GoFunc(fn GoFunction) GoExport { return GoExport{function: fn} }

// GoValue is an export that is a JSON value: the plugin reads it as the value itself, parsed once per process. Its Go value
// follows json.Marshal (a *delta.JsonObject keeps its key order).
func GoValue(value delta.JsonValue) GoExport { return GoExport{value: value, isValue: true} }

// GoPackage is a host package the Go app offers: the plugins' `require(Specifier)` (and an app module's `import`) answers a
// module with these exports. Specifier is the name the plugins' builds keep external (BuildOptions.HostPackages, a plugin's
// `chord.external`).
type GoPackage struct {
	Specifier string
	Exports   map[string]GoExport
}

// goPackageWire is a GoPackage as host.mjs imports it: the exports sorted by name, each a call by name or a value.
type goPackageWire struct {
	Specifier string         `json:"specifier"`
	Exports   []goExportWire `json:"exports"`
}

type goExportWire struct {
	Name string `json:"name"`
	// Call is the name of the Go function of a function export.
	Call string `json:"call,omitempty"`
	// Value is the JSON of a value export (`null` included).
	Value json.RawMessage `json:"value,omitempty"`
}

// goFunctionName is the name the process calls the function export of specifier by.
func goFunctionName(specifier, export string) string { return "pkg:" + specifier + "#" + export }

// goPackagesWire validates packages and returns them as the process imports them. A GoPackage cannot be offered when its
// specifier is empty, one that two packages or a HostPackages file share, or one of the host's own (`@earendil-works/chord`,
// `@earendil-works/chord/context`); nor when an export has no name, is neither GoFunc nor GoValue, or is a value that is not
// JSON.
func goPackagesWire(packages []GoPackage, modules HostPackages) ([]goPackageWire, error) {
	seen := map[string]bool{}
	wire := make([]goPackageWire, len(packages))
	for i, pkg := range packages {
		switch {
		case pkg.Specifier == "":
			return nil, errors.New("plugins.load: a GoPackage has no Specifier")
		case seen[pkg.Specifier]:
			return nil, fmt.Errorf("plugins.load: GoPackage %q is offered twice", pkg.Specifier)
		case pkg.Specifier == "@earendil-works/chord" || pkg.Specifier == "@earendil-works/chord/context":
			return nil, fmt.Errorf("plugins.load: GoPackage %q is the host's own package", pkg.Specifier)
		}
		if _, file := modules[pkg.Specifier]; file {
			return nil, fmt.Errorf("plugins.load: %q is both a GoPackage and a HostPackages file", pkg.Specifier)
		}
		seen[pkg.Specifier] = true
		names := slices.Sorted(maps.Keys(pkg.Exports))
		wire[i] = goPackageWire{Specifier: pkg.Specifier, Exports: make([]goExportWire, 0, len(names))}
		for _, name := range names {
			export := pkg.Exports[name]
			switch {
			case name == "":
				return nil, fmt.Errorf("plugins.load: GoPackage %q has an export with no name", pkg.Specifier)
			case export.function != nil:
				wire[i].Exports = append(wire[i].Exports, goExportWire{Name: name, Call: goFunctionName(pkg.Specifier, name)})
			case export.isValue:
				raw, err := json.Marshal(export.value)
				if err != nil {
					return nil, fmt.Errorf("plugins.load: GoPackage %q export %q is not JSON: %w", pkg.Specifier, name, err)
				}
				wire[i].Exports = append(wire[i].Exports, goExportWire{Name: name, Value: raw})
			default:
				return nil, fmt.Errorf("plugins.load: GoPackage %q export %q is neither GoFunc nor GoValue", pkg.Specifier, name)
			}
		}
	}
	return wire, nil
}

// handleGoPackages registers the functions of packages on h: the process calls each by goFunctionName.
func (h *NodeHost) handleGoPackages(packages []GoPackage) {
	for _, pkg := range packages {
		for name, export := range pkg.Exports {
			if export.function != nil {
				h.handleFunction(goFunctionName(pkg.Specifier, name), export.function)
			}
		}
	}
}
