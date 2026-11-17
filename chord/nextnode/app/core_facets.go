package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/src/node/app/app.ts (`themesFacet()`, `translationsFacet()` in the server's core) and
// packages/chord/src/node/app/worker.ts (`translationsFacet()` in the worker's core).
//
// pig divergence (D460): Pi loads the two registries' facets into the one host that loads the plugins. Here the plugins run in a Node
// subprocess beside the Go host that carries the kit's facets, and no registry or contribution crosses between the two hosts, so the
// facets the plugins' registries need are Pi's own (`translationsFacet`, `themesFacet`, vendored in the Node host), loaded as JavaScript
// core facets into that subprocess: a plugin's contribution to `Translations` or `Themes` is composed there, and the plugin that reads
// the registry (`self.registry(Translations)`) reads it there. The Go host's `chord.translations` and `chord.themes` stay beside them
// for the app's Go facets and the kit's own pages (their registry holds the Go facets' contributions, as D460 records).

// piFacet is one of Pi's own facets as a core facet of the Node host: its name (a plugin name) and the vendored function that makes it.
type piFacet struct{ name, function string }

var (
	// piTranslations is Pi's `translationsFacet` (`surfaces/translations.ts`), on the server's Node host and on each worker's.
	piTranslations = piFacet{"chord.translations", "translationsFacet"}
	// piThemes is Pi's `themesFacet` (`surfaces/themes.ts`), on the server's Node host only, as Pi's `worker.ts` loads none.
	piThemes = piFacet{"chord.themes", "themesFacet"}
)

// piCoreFacets writes into dir a module for each of facets, `export default <function>()` imported from Pi's package, and returns the
// core facets that name them for the Node host (plugins.PluginsLoad.Core, workers.WorkerLoad.NodeCore). The modules are the same at
// every start: the host re-imports them when its process restarts, so they stay in dir.
func piCoreFacets(dir string, facets ...piFacet) ([]plugins.CoreFacet, error) {
	core := make([]plugins.CoreFacet, len(facets))
	for i, facet := range facets {
		module := filepath.Join(dir, "."+facet.name+".mjs")
		text := fmt.Sprintf("import { %s } from \"@earendil-works/chord\";\nexport default %s();\n", facet.function, facet.function)
		if err := os.WriteFile(module, []byte(text), 0o600); err != nil {
			return nil, err
		}
		core[i] = plugins.CoreFacet{Name: facet.name, Module: module}
	}
	return core, nil
}
