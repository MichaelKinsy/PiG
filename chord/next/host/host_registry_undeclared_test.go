package host

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/src/registries/record.ts
// Ports packages/chord/test/registries/record.test.ts: "registries without a provider (slice 12 §6, testPlugin)" and
// "registry_undeclared (decision "Mistakes that wait are reported at once" (b))", both of which need a host.

// undeclaredReports is the registry_undeclared reports of errs, as registry and plugins.
func undeclaredReports(errs *hostErrors) [][2]any {
	var reports [][2]any
	for _, reported := range errs.All() {
		if reported.Kind == HostErrorKind(registries.ErrorRegistryUndeclared) {
			reports = append(reports, [2]any{reported.Registry.Registry, reported.Registry.Plugins})
		}
	}
	return reports
}

func contributingPlugin(plugin string) HostFacet {
	return rvPlugin(plugin, func(_ FacetHost, self FacetSelf) error {
		return self.Contribute(rvNames, plugin)
	})
}

func servingNames() HostFacet {
	return rvPlugin("serving", func(h FacetHost, _ FacetSelf) error { return rvServeNames(h) })
}

// Mirrors "registries without a provider (slice 12 §6, testPlugin) › lists a registry with contributions and no provider, with the newest version contributed".
func TestRegistriesWithoutAProviderListsARegistryWithContributionsAndNoProviderWithTheNewestVersionContributed(t *testing.T) {
	older, newer, served := rvRegistryOf("app.names", 1), rvRegistryOf("app.names", 3), rvRegistryOf("app.served", 1)
	host, _ := rvPlugins(t, HostOptions{},
		rvPlugin("a", func(_ FacetHost, self FacetSelf) error { return self.Contribute(older, "a") }),
		rvPlugin("b", func(_ FacetHost, self FacetSelf) error { return self.Contribute(newer, "b") }),
		rvPlugin("c", func(h FacetHost, _ FacetSelf) error {
			return h.Serve(served, registries.RegistryProvider[any, any]{Compose: func(registries.RegistryTable[any], registries.ComposeContext) (any, error) { return []any{}, nil }}, ServeOptions{})
		}))
	h := host.(*chordHost)
	var got []undeclaredRegistry
	h.enter(func() { got = h.undeclaredRegistries() })
	if want := []undeclaredRegistry{{Name: "app.names", Version: 3}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("undeclaredRegistries = %v, want %v", got, want)
	}
	closeHosts(t, host)
}

// Mirrors "registry_undeclared › reports at load's publication a registry with contributions and no provider, naming its contributors".
func TestRegistryUndeclaredReportsAtLoadsPublicationARegistryWithContributionsAndNoProviderNamingItsContributors(t *testing.T) {
	host, errs := rvPlugins(t, HostOptions{}, contributingPlugin("b"), contributingPlugin("a"))
	want := [][2]any{{"app.names", []string{"a", "b"}}}
	if got := undeclaredReports(errs); !reflect.DeepEqual(got, want) {
		t.Fatalf("reports = %v, want %v", got, want)
	}
	closeHosts(t, host)
}

// Mirrors "registry_undeclared › reports again at a reload only when the contributors change; a provider ends the episode".
func TestRegistryUndeclaredReportsAgainAtAReloadOnlyWhenTheContributorsChangeAProviderEndsTheEpisode(t *testing.T) {
	host, errs := rvPlugins(t, HostOptions{}, contributingPlugin("a"))
	reload := func(plugins ...HostFacet) {
		t.Helper()
		_, err := host.Reload(plugins, background, ReloadOptions{})
		must(t, err)
	}
	a, b := contributingPlugin("a"), contributingPlugin("b")
	// Unchanged contributors: no repeat per reload.
	reload(a, rvPlugin("other", func(FacetHost, FacetSelf) error { return nil }))
	if got := undeclaredReports(errs); len(got) != 1 {
		t.Fatalf("reports = %v, want 1", got)
	}
	reload(a, b)
	if got, want := undeclaredReports(errs)[1], ([2]any{"app.names", []string{"a", "b"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("second report = %v, want %v", got, want)
	}
	// Served: nothing reported; removed again: a new episode, reported again.
	reload(a, b, servingNames())
	if got := undeclaredReports(errs); len(got) != 2 {
		t.Fatalf("reports = %v, want 2", got)
	}
	reload(a, b)
	if got, want := undeclaredReports(errs)[2], ([2]any{"app.names", []string{"a", "b"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("third report = %v, want %v", got, want)
	}
	closeHosts(t, host)
}

// namesSetter serves Setter, a host-lifetime object contributing a writable string to Names.
func namesSetter(initial string) func(h FacetHost, _ FacetSelf) error {
	return func(h FacetHost, _ FacetSelf) error {
		return h.Serve(rvSetterType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			value := self.Writable(initial)
			self.(StoreSelf).Contribute(rvNames, value)
			return rvSetterImpl(value), nil
		}, ServeOptions{})
	}
}

// Mirrors "registry_undeclared › reports a host-lifetime object's contribution entering a registry nobody serves, once across its restarts".
func TestRegistryUndeclaredReportsAHostLifetimeObjectsContributionEnteringARegistryNobodyServesOnceAcrossItsRestarts(t *testing.T) {
	host, errs := rvPlugins(t, HostOptions{}, rvPlugin("@app/setter", namesSetter("x")))
	want := [][2]any{{"app.names", []string{"@app/setter"}}}
	if got := undeclaredReports(errs); !reflect.DeepEqual(got, want) {
		t.Fatalf("reports = %v, want %v", got, want)
	}
	_, err := host.Reload([]HostFacet{rvPlugin("@app/setter", namesSetter("y"))}, background, ReloadOptions{})
	must(t, err)
	turn(host)
	if got := undeclaredReports(errs); len(got) != 1 {
		t.Fatalf("reports after the restart = %v, want 1", got)
	}
	closeHosts(t, host)
}

// Mirrors "registry_undeclared › reports nothing for a served registry or one without contributions".
func TestRegistryUndeclaredReportsNothingForAServedRegistryOrOneWithoutContributions(t *testing.T) {
	host, errs := rvPlugins(t, HostOptions{}, contributingPlugin("a"), servingNames())
	_, err := host.Reload([]HostFacet{servingNames()}, background, ReloadOptions{})
	must(t, err)
	if got := undeclaredReports(errs); len(got) != 0 {
		t.Fatalf("reports = %v, want none", got)
	}
	closeHosts(t, host)
}
