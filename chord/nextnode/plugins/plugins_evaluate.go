package plugins

import (
	"errors"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/next-node/plugins/evaluate.ts (HostPackages, EvaluateOptions, the script's filename, `fileOf`
// behind `pluginFile`). Evaluating (slice 7c §3): a built script becomes the plugin's facet, run under a stable filename
// per (plugin, kind, digest) and given the host packages (one instance per process) and its plugin's directory. Upstream
// runs it with `vm.Script`; the evaluation is the consumer-owned Evaluator seam, and its default export check (the default
// export is a facet) is the evaluator's error.

// HostPackages are the host packages by specifier, each what the app's runtime gives a script that imports it.
type HostPackages map[string]any

// EvaluateOptions is what EvaluateFacet takes besides the script: the host packages and the plugin it was built from.
type EvaluateOptions struct {
	HostPackages HostPackages
	Plugin       Plugin
}

// Evaluator evaluates a built script to its facet. It is upstream's `evaluateFacet`, owned by the consumer.
type Evaluator interface {
	// EvaluateFacet evaluates built, a BuiltFacetScript of options.Plugin, and returns its facet. It returns what the script
	// throws while it is evaluated, an import it does not answer, a declaration error when its default export is not a
	// facet, and an error for a failed build (which has no script).
	EvaluateFacet(built BuiltFacet, options EvaluateOptions) (host.Facet, error)
}

// ScriptFilename is the stable filename a script is evaluated under, per plugin, kind and digest: the plugin's name, the
// base name of its facet file of the kind (`<kind>.js` when it has none) and the digest.
func ScriptFilename(built BuiltFacet, plugin Plugin) string {
	file, ok := plugin.Facets[built.Facet]
	if !ok {
		file = built.Facet + ".js"
	}
	return "/chord-plugins/" + plugin.Name + "/" + filepath.Base(file) + "?" + built.Digest
}

// PluginFile is the absolute path of path inside plugin's directory (Pi's `pluginFile(path)` of a facet script, which an
// Evaluator binds to the plugin it evaluates); one out of the directory is refused.
func PluginFile(plugin Plugin, path string) (string, error) {
	file := path
	if !filepath.IsAbs(file) {
		file = filepath.Join(plugin.Dir, path)
	}
	rest, err := relative(plugin.Dir, file)
	if err != nil || hasDotDotPrefix(rest) || filepath.IsAbs(rest) {
		return "", errors.New(`pluginFile("` + path + `"): outside the directory of plugin "` + plugin.Name + `"`)
	}
	return filepath.Clean(file), nil
}
