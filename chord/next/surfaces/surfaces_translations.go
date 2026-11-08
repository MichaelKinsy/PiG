package surfaces

import (
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/surfaces/translations.ts.

// Translation is one catalog: the messages of Namespace in Locale; Override true replaces the plugin's own for that
// locale.
type Translation struct {
	Namespace string
	Locale    string
	// Messages may be partial: a missing key falls back (the locale's base, then the namespace's first catalog).
	Messages Catalog
	Override bool
}

// TranslationIndex is the composed catalogs: the one used per namespace and canonical locale, with its plugin; every
// locale offered.
type TranslationIndex struct {
	Catalogs map[string]map[string]registries.TableItem[Translation]
	// Locales is every locale some catalog offers, canonical (`pt-BR`), sorted.
	Locales []string
}

// Translations is the registry of the catalogs plugins contribute, a list per contributor; served on the server, pages
// and terminals by the kit.
var Translations = mustRegistry[[]Translation, *TranslationIndex]("chord.translations")

func mustRegistry[C, V any](name string) registries.Registry[C, V] {
	registry, err := registries.DefineRegistry[C, V](name, registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		panic(err)
	}
	return registry
}

// NoTranslations is what a host's language has before its `Translations` value arrives.
var NoTranslations = &TranslationIndex{Catalogs: map[string]map[string]registries.TableItem[Translation]{}}

// TranslationsFacet is the kit's facet serving `Translations` on a host.
func TranslationsFacet() host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		provider := registries.RegistryProvider[[]Translation, *TranslationIndex]{Compose: ComposeTranslations}
		return h.Serve(registries.Registry[any, any](Translations), provider.Erase(), host.ServeOptions{})
	})
}

// ComposeTranslations is the index of table: per namespace and locale the winning catalog; conflicts and invalid
// locales reported. It never fails (upstream's compose is total).
func ComposeTranslations(table registries.RegistryTable[[]Translation], ctx registries.ComposeContext) (*TranslationIndex, error) {
	catalogs := map[string]map[string]registries.TableItem[Translation]{}
	var locales []string
	for _, entry := range table {
		for _, translation := range entry.Value {
			locale, ok := CanonicalLocale(translation.Locale)
			if !ok {
				ctx.Report(registries.RegistryConflict{
					Message: fmt.Sprintf("%q is no locale (%s)", translation.Locale, translation.Namespace),
					Plugins: []string{entry.Plugin},
				})
				continue
			}
			if !slices.Contains(locales, locale) {
				locales = append(locales, locale)
			}
			byLocale := catalogs[translation.Namespace]
			if byLocale == nil {
				byLocale = map[string]registries.TableItem[Translation]{}
				catalogs[translation.Namespace] = byLocale
			}
			taken, has := byLocale[locale]
			switch {
			case !has || (translation.Override && !taken.Value.Override):
				byLocale[locale] = registries.TableItem[Translation]{Value: translation, Plugin: entry.Plugin}
			case translation.Override == taken.Value.Override:
				ctx.Report(registries.RegistryConflict{
					Message: fmt.Sprintf("%s in %s is contributed twice", translation.Namespace, locale),
					Plugins: []string{taken.Plugin, entry.Plugin},
				})
			}
		}
	}
	slices.Sort(locales)
	return &TranslationIndex{Catalogs: catalogs, Locales: locales}, nil
}
