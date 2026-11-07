package surfaces

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/test/surfaces/translations.test.ts

func fr(word string, override bool) Translation {
	return Translation{Namespace: "notes", Locale: "fr", Messages: Catalog{"newNote": word}, Override: override}
}

type contribution struct {
	plugin string
	value  []Translation
}

// compose is the index of contributions (in plugin-name order) and its reported conflicts.
func compose(t testing.TB, contributions ...contribution) (*TranslationIndex, []registries.RegistryConflict, func(namespace, locale string) any) {
	t.Helper()
	var conflicts []registries.RegistryConflict
	var table registries.RegistryTable[[]Translation]
	for _, c := range contributions {
		table = append(table, registries.RegistryEntry[[]Translation]{Plugin: c.plugin, Version: 1, Value: c.value})
	}
	index, err := ComposeTranslations(table, registries.ComposeContext{Report: func(c registries.RegistryConflict) { conflicts = append(conflicts, c) }})
	if err != nil {
		t.Fatal(err)
	}
	word := func(namespace, locale string) any {
		return index.Catalogs[namespace][locale].Value.Messages["newNote"]
	}
	return index, conflicts, word
}

func TestComposeKeepsTheFirstByPluginNameReportingTheOther(t *testing.T) {
	_, conflicts, word := compose(t,
		contribution{"@a/fr", []Translation{fr("Nouvelle note", false)}},
		contribution{"@b/fr", []Translation{fr("Note neuve", false)}})
	if got := word("notes", "fr"); got != "Nouvelle note" {
		t.Errorf("word = %v", got)
	}
	want := []registries.RegistryConflict{{Message: "notes in fr is contributed twice", Plugins: []string{"@a/fr", "@b/fr"}}}
	if !reflect.DeepEqual(conflicts, want) {
		t.Errorf("conflicts = %v, want %v", conflicts, want)
	}
}

func TestComposeLetsOverrideWinOverACatalogWithoutIt(t *testing.T) {
	_, conflicts, word := compose(t,
		contribution{"@a/notes", []Translation{fr("Note", false)}},
		contribution{"@b/better", []Translation{fr("Nouvelle note", true)}})
	if got := word("notes", "fr"); got != "Nouvelle note" || len(conflicts) != 0 {
		t.Errorf("word = %v, conflicts = %v", got, conflicts)
	}
}

func TestComposeReportsTwoOverridesTheFirstByPluginNameWinning(t *testing.T) {
	_, conflicts, word := compose(t,
		contribution{"@a/x", []Translation{fr("A", true)}},
		contribution{"@b/y", []Translation{fr("B", true)}})
	if got := word("notes", "fr"); got != "A" {
		t.Errorf("word = %v", got)
	}
	if len(conflicts) != 1 || !reflect.DeepEqual(conflicts[0].Plugins, []string{"@a/x", "@b/y"}) {
		t.Errorf("conflicts = %v", conflicts)
	}
}

func TestComposeKeysCatalogsByCanonicalLocaleLeavesInvalidOnesOutAndListsTheLocalesOffered(t *testing.T) {
	notes, err := DefineMessages("notes", LocaleCatalog{"en", Catalog{"newNote": "New note"}}, LocaleCatalog{"pt-br", Catalog{"newNote": "Nova nota"}})
	if err != nil {
		t.Fatal(err)
	}
	index, conflicts, word := compose(t,
		contribution{"@a/notes", notes.Translations},
		contribution{"@b/bad", []Translation{{Namespace: "notes", Locale: "not a tag", Messages: Catalog{}}}})
	if got := word("notes", "pt-BR"); got != "Nova nota" {
		t.Errorf("word = %v", got)
	}
	if want := []string{"en", "pt-BR"}; !reflect.DeepEqual(index.Locales, want) {
		t.Errorf("locales = %v, want %v", index.Locales, want)
	}
	want := []registries.RegistryConflict{{Message: `"not a tag" is no locale (notes)`, Plugins: []string{"@b/bad"}}}
	if !reflect.DeepEqual(conflicts, want) {
		t.Errorf("conflicts = %v, want %v", conflicts, want)
	}
}

func TestTranslationsFacetServesTheRegistryComposingItsPluginsContributions(t *testing.T) {
	h, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	ctx := context.Background()
	report, err := h.Load([]host.HostFacet{
		{Name: "chord.translations", Facet: TranslationsFacet()},
		{Name: "@test/fr", Facet: host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
			return self.Contribute(registries.Registry[any, any](Translations), []Translation{fr("Nouvelle note", false)})
		})},
	}, ctx)
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("load: %v, %v", err, report.Failed)
	}
	value, err := h.Registry(ctx, registries.Registry[any, any](Translations), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	index := value.Value().(*TranslationIndex)
	if want := []string{"fr"}; !reflect.DeepEqual(index.Locales, want) {
		t.Errorf("locales = %v, want %v", index.Locales, want)
	}
}
