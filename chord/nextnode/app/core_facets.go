package app

import "github.com/MichaelKinsy/PiG/chord/nextnode/plugins"

// Ports packages/chord/src/node/app/app.ts (`themesFacet()`, `translationsFacet()` in the server's core) and
// packages/chord/src/node/app/worker.ts (`translationsFacet()` in the worker's core).
//
// pig divergence (D460): Pi loads the two registries' facets into the one host that loads the plugins. Here the plugins run in a Node
// subprocess beside the Go host that carries the kit's facets, and no registry or contribution crosses between the two hosts, so the
// facets the plugins' registries need are Pi's own (`translationsFacet`, `themesFacet`, vendored in the Node host), made by the Node host
// as core facets before the plugins load (plugins.CoreFacet.Export): a plugin's contribution to `Translations` or `Themes` is composed
// there, and the plugin that reads the registry (`self.registry(Translations)`) reads it there. The Go host's `chord.translations` and
// `chord.themes` stay beside them for the app's Go facets (their registry holds the Go facets' contributions, as D460 records).

var (
	// piTranslations is Pi's `translationsFacet` (`surfaces/translations.ts`), on the server's Node host and on each worker's.
	piTranslations = plugins.CoreFacet{Name: "chord.translations", Export: "translationsFacet"}
	// piThemes is Pi's `themesFacet` (`surfaces/themes.ts`), on the server's Node host only, as Pi's `worker.ts` loads none.
	piThemes = plugins.CoreFacet{Name: "chord.themes", Export: "themesFacet"}
)
