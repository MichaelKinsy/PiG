package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Ports packages/chord/test/surfaces/translations.test.ts `the kit's facet` "serves the registry on a host, composing its plugins'
// contributions" (Pi main f6600b58, :68): a plugin contributes a catalog to `Translations`, and the registry served on the host lists
// its locale, here read by ANOTHER plugin's object through `self.registry(Translations)`, the way a worker that renders final text
// reads it (`node/app/worker.ts:4`). Pi loads `translationsFacet()` into the one host that loads the plugins (`node/app/app.ts:386` on
// the server, `node/app/worker.ts:85` on a worker) and `themesFacet()` on the server only (`app.ts:384`; `worker.ts` has none). In pig the
// plugins run in a Node subprocess beside the Go host that carries the kit's facets (D460), so the facets the plugins' registries need
// are Pi's own, loaded as JavaScript core facets into that subprocess. The contributions of the app's Go facets stay on the Go host's
// registry (D460).
//
// The reader serves a type whose member is the registry's index as text; the tests read the member of the object a Go host holds.

// registryRead is the plugin that reads a registry: a local type (server) or a remote type placed on the project worker.
func registryRead(registry, expression, kind string) string {
	head := `import { defineFacet, hostKind, local, remote, str, ` + registry + ` } from "@test/app/v1";
const Project = hostKind("project", { id: str() });
`
	if kind == "server" {
		return head + `const Read = local("test.reg.` + strings.ToLower(registry) + `", { version: 1, params: {}, members: { v: str() } });
export default defineFacet((host) => host.serve(Read, (_params, self) => ({ v: self.derive([self.registry(` + registry + `)], ([value]) => ` + expression + `) })));`
	}
	return head + `const Read = remote("test.reg.worker.` + strings.ToLower(registry) + `", { version: 1, params: { id: str() }, hostAddress: ({ id }) => Project({ id }), variants: { main: { state: { v: str() } } } });
export default defineFacet((host) => host.serve(Read, (_params, _principal, self) => ({ variant: "main", members: { v: self.derive([self.registry(` + registry + `)], ([value]) => ` + expression + `) } })));`
}

// localesOf and themesOf are the expressions of the readers: the locales offered, the ids of the themes.
const (
	localesOf = `JSON.stringify([...value.locales])`
	themesOf  = `JSON.stringify(value.map((theme) => theme.id))`
)

// contributesCatalog is a plugin of kind (`server.ts` or `worker.ts`) contributing a catalog of locale, as Pi's `fr(...)` does.
func contributesCatalog(file, locale string) map[string]string {
	return map[string]string{file: `import { defineFacet, Translations } from "@test/app/v1";
export default defineFacet((_host, self) => self.contribute(Translations, [{ namespace: "notes", locale: "` + locale + `", messages: { newNote: "Nouvelle note" } }]));`}
}

// contributesTheme is a plugin contributing a theme of id to `Themes` (a plugin's, as the app's own pages' are).
func contributesTheme(file, id string) map[string]string {
	return map[string]string{file: `import { defineFacet, Themes } from "@test/app/v1";
export default defineFacet((_host, self) => self.contribute(Themes, { id: "` + id + `", name: "Plugin", mode: "dark", colors: { accent: "#111111", bg: "#ffffff", fg: "#000000", muted: "#888888", error: "#ff0000", warning: "#ffff00", success: "#00ff00" } }));`}
}

// readyShort is the ready timeout of a test whose Get is expected to fail: a plugin object that reads a registry nobody serves never
// becomes ready, and the test sees it after this long.
func readyShort(o *AppOptions) { o.Timeouts.ReadyTimeout = 5 * time.Second }

var (
	readLocales = mustLocal("test.reg.translations", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("v", contracts.Str())}})
	readThemes  = mustLocal("test.reg.themes", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("v", contracts.Str())}})
)

// workerRead is the Go form of the worker reader's remote type of registry (a lower-case name of the reader).
func workerRead(registry string) *contracts.Contract {
	return mustRemote("test.reg.worker."+registry, contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			return project.Address(delta.JsonObjectOf("id", params.Value("id"))), nil
		},
		State: []contracts.RemoteStateEntry{contracts.StateEntry("v", contracts.Str())},
	})
}

// until polls the member of a reader every 10 ms until its value is want (as the Pi tests' `while (...) await sleep(10)`), failing with
// the last value it had.
func until(t testing.TB, what string, member interface {
	Value() (delta.JsonValue, error)
}, want string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var got delta.JsonValue
	var err error
	for {
		if got, err = member.Value(); err == nil && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: v = %v (%v), want %s", what, got, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func serverReader(t testing.TB, s started, contract *contracts.Contract) interface {
	Value() (delta.JsonValue, error)
} {
	t.Helper()
	ref, err := s.app.Node.Get(background, contract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatalf("the plugin's reader of %s never became ready: %v", contract.Name, err)
	}
	t.Cleanup(ref.Release)
	return ref.Member("v")
}

func workerReader(t testing.TB, s started, contract *contracts.Contract) interface {
	Value() (delta.JsonValue, error)
} {
	t.Helper()
	ref, err := s.app.Host.Get(background, contract, delta.JsonObjectOf("id", "p1"), host.GetOptions{})
	if err != nil {
		t.Fatalf("the worker plugin's reader of %s never became ready: %v", contract.Name, err)
	}
	t.Cleanup(ref.Release)
	return ref.Member("v")
}

// Pi translations.test.ts:68 on the server (`app.ts:386`): the registry a plugin's object reads lists what another plugin contributed.
func TestAppServerPluginReadsAnotherServerPluginsTranslationsFromItsRegistry(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{
		"french": contributesCatalog("server.ts", "fr"),
		"reader": {"server.ts": registryRead("Translations", localesOf, "server")},
	}, readyShort)
	until(t, "locales of the server's Translations", serverReader(t, s, readLocales), `["fr"]`)
}

// Pi translations.test.ts:68 on a worker (`worker.ts:85`).
func TestAppWorkerPluginReadsAnotherWorkerPluginsTranslationsFromItsRegistry(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{
		"french": contributesCatalog("worker.ts", "fr"),
		"reader": {"worker.ts": registryRead("Translations", localesOf, "worker")},
	}, func(o *AppOptions) {
		readyShort(o)
		projectOptions(t)(o)
	})
	until(t, "locales of the worker's Translations", workerReader(t, s, workerRead("translations")), `["fr"]`)
}

// The same through `Themes` (`themes.ts:35`, served on the server by `app.ts:384`).
func TestAppServerPluginReadsAnotherServerPluginsThemesFromItsRegistry(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{
		"dusk":   contributesTheme("server.ts", "dusk"),
		"reader": {"server.ts": registryRead("Themes", themesOf, "server")},
	}, readyShort)
	until(t, "ids of the server's Themes", serverReader(t, s, readThemes), `["dusk"]`)
}

// Pi's worker loads no themes facet (`worker.ts:76-85` lists the worker-info, files, data and translations facets): a worker plugin that
// reads `Themes` gets what Pi's gets, a failed registry, `registry chord.themes: no plugin serves it here` (`registries/record.ts:199`),
// not a registry some other host serves.
func TestAppWorkerPluginReadingThemesGetsNoPluginServesItHereAsPiWorkerHasNoThemesFacet(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{
		"dusk":   contributesTheme("worker.ts", "dusk"),
		"reader": {"worker.ts": registryRead("Themes", themesOf, "worker")},
	}, func(o *AppOptions) {
		readyShort(o)
		projectOptions(t)(o)
	})
	s.errors.expect()
	ref, err := s.app.Host.Get(background, workerRead("themes"), delta.JsonObjectOf("id", "p1"), host.GetOptions{})
	if err == nil {
		ref.Release()
		t.Fatal("a worker plugin read Themes: Pi's worker serves none")
	}
	if !strings.Contains(err.Error(), "registry chord.themes: no plugin serves it here") {
		t.Fatalf("Get error = %v, want Pi's `registry chord.themes: no plugin serves it here`", err)
	}
}

// Unloading the contributing plugin removes its catalog from the registry the reader reads (the Node record's `remove`).
func TestAppRemovingTheContributingPluginRemovesItsCatalogFromTheReadersRegistry(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{
		"french": contributesCatalog("server.ts", "fr"),
		"reader": {"server.ts": registryRead("Translations", localesOf, "server")},
	}, readyShort)
	locales := serverReader(t, s, readLocales)
	until(t, "locales before the removal", locales, `["fr"]`)
	if failures := rolloutFailures(s.removeAndApply(t, "french")); len(failures) != 0 {
		t.Fatalf("rollout failures = %v", failures)
	}
	until(t, "locales after the removal", locales, `[]`)
}

// A reload of the plugins keeps the contributions true: the contributing plugin reloaded with another catalog, then the reader reloaded.
func TestAppReloadingThePluginsKeepsTheReadersTranslationsTrue(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{
		"french": contributesCatalog("server.ts", "fr"),
		"reader": {"server.ts": registryRead("Translations", localesOf, "server")},
	}, readyShort)
	locales := serverReader(t, s, readLocales)
	until(t, "locales before the reload", locales, `["fr"]`)
	if failures := rolloutFailures(s.editAndApply(t, "french", contributesCatalog("server.ts", "de"))); len(failures) != 0 {
		t.Fatalf("rollout failures after editing the contributor = %v", failures)
	}
	until(t, "locales after the contributor's reload", locales, `["de"]`)
	if failures := rolloutFailures(s.editAndApply(t, "reader", map[string]string{"server.ts": registryRead("Translations", localesOf, "server") + "\n// reloaded"})); len(failures) != 0 {
		t.Fatalf("rollout failures after editing the reader = %v", failures)
	}
	until(t, "locales after the reader's reload", serverReader(t, s, readLocales), `["de"]`)
}

// What remains different (D460): the app's Go facets and the plugins' Node host each have a registry of their own. A Go facet's
// contribution to `Translations` is read by a Go reader of the Go host and not by a plugin; a plugin's is read by a plugin and not
// by the Go host's reader. (Pi's one host serves both to both; no facet of the kit itself contributes to either registry.)
func TestAppGoFacetsAndPluginsEachReadOnlyTheirOwnHostsTranslations(t *testing.T) {
	german := host.HostFacet{Name: "go-catalog", Facet: host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](surfaces.Translations), []surfaces.Translation{{Namespace: "go.page", Locale: "de", Messages: surfaces.Catalog{"hello": "hallo"}}})
	})}
	s := startTestApp(t, map[string]map[string]string{
		"french": contributesCatalog("server.ts", "fr"),
		"reader": {"server.ts": registryRead("Translations", localesOf, "server")},
	}, func(o *AppOptions) {
		readyShort(o)
		o.Facets = []host.HostFacet{german}
	})
	until(t, "locales a plugin reads", serverReader(t, s, readLocales), `["fr"]`)
	reg, err := s.app.Host.Registry(background, registries.Registry[any, any](surfaces.Translations), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	index, ok := reg.Value().(*surfaces.TranslationIndex)
	if !ok {
		t.Fatalf("the Go host's Translations = %T", reg.Value())
	}
	if !slices.Equal(index.Locales, []string{"de"}) {
		t.Fatalf("locales the Go host's reader reads = %v, want the Go facet's [de] alone", index.Locales)
	}
}

// Pi's own rule, shown by the core facet: a registry is served by one facet per host. A plugin that serves `Translations` again
// (`translationsFacet()`) beside the one the app loads fails its load as in Pi (`registries/record.ts`), `registry chord.translations
// is served twice (plugin "chord.translations")`: it is the Node host's rule, not a clash with the Go host's facet of the same name.
func TestAppFailsAPluginThatServesTranslationsAgainBesideTheAppsFacetAsPiDoes(t *testing.T) {
	options, _ := testOptions(t, nil)
	writePlugin(t, options.Root, "again", "@test/again", map[string]string{"server.ts": `import { translationsFacet } from "@test/app/v1";
export default translationsFacet();`})
	app, err := StartServer(options, background)
	if err == nil {
		closeAndWait(t, app)
		t.Fatal("a plugin served Translations beside the app's facet: Pi's host refuses it")
	}
	if !strings.Contains(err.Error(), `registry chord.translations is served twice (plugin "chord.translations")`) {
		t.Fatalf("start: %v", err)
	}
}
