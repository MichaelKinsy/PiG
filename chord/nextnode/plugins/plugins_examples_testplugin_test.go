package plugins

// pi: packages/chord/test/examples/52-test-plugin/main.ts

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports examples/52-test-plugin/main.ts: testing a plugin alone. TestPlugin builds the stats plugin as production does (the same
// build, the app's API checked), loads it on an in-memory host beside a stub of the notes plugin (an ordinary Go facet serving
// `Notes` with fake data, bridged to the plugin's host), reads its indexer from the collected `Indexers` (a registry of the app
// no stub serves), lists the `Tags` contract its requests wait for unserved, and runs code outside objects as the host's
// principal and as a user. The example's modules are example 49's app (`app/v3.ts`, `app/v3/worker.ts`) as the test writes them: Chord
// re-exported, and the worker module's `Indexers` (the plugins' own imports are the same specifiers).
func TestExample52TestPlugin(t *testing.T) {
	cache := t.TempDir()
	root := exCopy(t, "52-test-plugin", "plugins")

	// The contracts the test uses, declared again with Go's builders (contracts are data: the same name, version and schema bind).
	project := contracts.Params{contracts.Prop("project", contracts.Str())}
	notes := mustContract(contracts.Local("acme.notes", contracts.LocalDeclaration{
		Version: 1, Params: project, Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())},
	}))
	stats := mustContract(contracts.Local("acme.stats", contracts.LocalDeclaration{
		Version: 1, Params: project, Members: []contracts.LocalEntry{contracts.Mem("words", contracts.Int())},
	}))
	tagLine := mustContract(contracts.Local("acme.tag-line", contracts.LocalDeclaration{
		Version: 1, Params: project, Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())},
	}))

	// 1. A stub of the notes plugin: an ordinary facet serving `Notes` with a fixed text for every project.
	notesStub := TestStub{
		Name: "@acme/notes", Contracts: []*contracts.Contract{notes},
		Facet: host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(notes, func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				text := params.Value("project").(string) + ": buy milk, call bob, ship it"
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"text": self.Derive(nil, func([]any) (delta.JsonValue, error) { return text, nil })},
				}}, nil
			}, host.ServeOptions{})
		}),
	}

	// 2. The plugin, tested alone: built against the app's API, loaded beside the stub as a worker host of project p1.
	worker := delta.JsonObjectOf("kind", "worker", "project", "p1")
	tested := tpPlugin(t, filepath.Join(root, "plugins", "stats"), TestPluginOptions{
		Kind: "worker", Kinds: []string{"server", "worker", "web"},
		Api:     &model.AppApi{Name: exAcmeApi, Versions: []string{"v3"}},
		Modules: tpAppModules(t, cache, `defineRegistry("acme.indexers", { version: 1 })`, "Indexers"), Stubs: []TestStub{notesStub},
		Principal: worker, Node: NodeOptions{CacheDir: cache},
	})

	var printed []string
	// 3. Its objects are live: get them, read their state.
	words := tpValue(t, tested, stats, delta.JsonObjectOf("project", "p1"), "words")
	printed = append(printed, "1. words in p1: "+tpJSON(t, words))

	// 4. What it contributed to `Indexers`, a registry of the app no stub serves: collected by the helper.
	indexers, err := tested.Contributions("acme.indexers", background)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range indexers {
		names = append(names, entry.Value.(*delta.JsonObject).Value("name").(string)+" ("+entry.Plugin+")")
	}
	printed = append(printed, "2. indexers: "+strings.Join(names, ", "))

	// 5. A contract nobody serves: its requests wait, and the helper names it, so the test knows what to stub next.
	tags, err := tested.Host.Hold(background, tagLine, delta.JsonObjectOf("project", "p1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tags.Release)
	eventually(t, 30*time.Second, "the tag line waits for acme.tags", func() bool {
		unserved, err := tested.Unserved(background)
		return err == nil && slices.Equal(unserved, []string{"acme.tags"})
	})
	unserved, err := tested.Unserved(background)
	if err != nil {
		t.Fatal(err)
	}
	printed = append(printed, "3. waiting for: "+strings.Join(unserved, ", ")+" | tag line: "+string(tags.State().Value().Kind))

	// 6. Code outside objects as another principal (a user), and as the host's own.
	own, err := tested.RootPrincipal(nil, background)
	if err != nil {
		t.Fatal(err)
	}
	user := delta.JsonObjectOf("kind", "user", "id", "u1")
	as, err := tested.RootPrincipal(user, background)
	if err != nil {
		t.Fatal(err)
	}
	printed = append(printed, "4. root as the host: "+tpJSON(t, own), "   root as a user: "+tpJSON(t, as))

	want := []string{
		"1. words in p1: 7",
		"2. indexers: word count (@acme/stats)",
		"3. waiting for: acme.tags | tag line: failed",
		`4. root as the host: {"kind":"worker","project":"p1"}`,
		`   root as a user: {"kind":"user","id":"u1"}`,
	}
	if !slices.Equal(printed, want) {
		t.Fatalf("printed:\n%s\nwant:\n%s", strings.Join(printed, "\n"), strings.Join(want, "\n"))
	}
	// What the host reported is the first load's `registry_undeclared` for `Indexers`, which nobody served until the helper did.
	for _, event := range tested.Errors() {
		if event.Kind != "registry_undeclared" || event.Registry != "acme.indexers" {
			t.Fatalf("the host reported %+v", event)
		}
	}
}
