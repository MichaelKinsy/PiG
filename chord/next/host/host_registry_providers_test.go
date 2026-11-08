package host

import (
	"fmt"
	"reflect"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/src/next/registries/registry.ts
// Mirrors "providers" of packages/chord/test/next/registries/registry.test.ts, on a loaded host.

var provNames = rvRegistryOf("app.names", 1)

func provLength(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
	return float64(len(table)), nil
}

func provServe(h FacetHost, _ FacetSelf) error {
	return h.Serve(provNames, registries.RegistryProvider[any, any]{Compose: provLength}, ServeOptions{})
}

// Mirrors "providers › one per host: a second serve leaves its plugin out at load, reported".
func TestProvidersOnePerHostASecondServeLeavesItsPluginOutAtLoadReported(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	report, err := host.Load([]HostFacet{rvPlugin("@app/a", provServe), rvPlugin("@app/b", provServe)}, background)
	must(t, err)
	want := [][2]string{{"@app/b", `plugin "@app/b": registry app.names is served twice (plugin "@app/a")`}}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	must(t, host.Close(background))
}

// Mirrors "providers › a provider without compose leaves its plugin out at load (plugin code is not type-checked)".
func TestProvidersAProviderWithoutComposeLeavesItsPluginOutAtLoad(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	report, err := host.Load([]HostFacet{rvPlugin("@app/a", func(h FacetHost, _ FacetSelf) error {
		return h.Serve(provNames, registries.RegistryProvider[any, any]{}, ServeOptions{})
	})}, background)
	must(t, err)
	want := [][2]string{{"@app/a", `plugin "@app/a": registry app.names: compose is a function`}}
	if got := failedMessages(report.Failed); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed = %v, want %v", got, want)
	}
	must(t, host.Close(background))
}

// Mirrors "providers › a registry whose provider comes with `load` gives `host.registry` its value".
func TestProvidersARegistryWhoseProviderComesWithLoadGivesHostRegistryItsValue(t *testing.T) {
	host := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(host.Load([]HostFacet{rvPlugin("@app/a", provServe)}, background))
	value, err := host.Registry(background, provNames, GetOptions{})
	must(t, err)
	if got, thrown := rvRead(value); thrown != nil || got != 0.0 {
		t.Fatalf("value = %v, %v, want 0", got, thrown)
	}
	must(t, host.Close(background))
}

// Mirrors "providers › are served only while the setup runs".
func TestProvidersAreServedOnlyWhileTheSetupRuns(t *testing.T) {
	var later FacetHost
	host := newHost(t, HostOptions{OnError: noopOnError})
	mustLoad(t)(host.Load([]HostFacet{rvPlugin("@app/a", func(h FacetHost, _ FacetSelf) error {
		later = h
		return nil
	})}, background))
	err := later.Serve(provNames, registries.RegistryProvider[any, any]{Compose: provLength}, ServeOptions{})
	if err == nil || err.Error() != "@app/a: host.serve is valid only in the facet's setup" {
		t.Fatalf("err = %v", err)
	}
	must(t, host.Close(background))
}

// Mirrors "the table's memory › keeps nothing of a closed host: records, tables and followed contributions go with it".
func TestTheTablesMemoryKeepsNothingOfAClosedHostRecordsTablesAndFollowedContributionsGoWithIt(t *testing.T) {
	doc, err := documents.DefineState("app.kept", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("on", contracts.Bool())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("on", true) },
	})
	must(t, err)
	kept := mustContract(contracts.Local("app.kept", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))
	refs := func() (weak.Pointer[registries.RegistryValueSource], weak.Pointer[chordHost]) {
		host := newHost(t, HostOptions{OnError: noopOnError})
		mustLoad(t)(host.Load([]HostFacet{rvPlugin("@app/kept", func(h FacetHost, _ FacetSelf) error {
			if err := rvServeNames(h); err != nil {
				return err
			}
			return h.Serve(kept, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				document := stateOf(self, doc)
				self.(StoreSelf).Contribute(rvNames, self.Derive([]any{document.Member()}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprint(values[0].(*delta.JsonObject).Value("on")), nil
				}))
				return exImpl(nil, nil), nil
			}, ServeOptions{})
		})}, background))
		names, err := host.Registry(background, rvNames, GetOptions{})
		must(t, err)
		must(t, host.Close(background))
		return weak.Make(names), weak.Make(host.(*chordHost))
	}
	names, host := refs()
	expectCollected(t, names)
	expectCollected(t, host)
}
