package host

import (
	"slices"
	"testing"
)

// Member-level checks for Pi declarations that no Pi test case names directly; each cites the Pi 1.2.0 line that
// defines or uses the member.

// packages/chord/src/declare.ts:37 (Resource.key), set from the declared name at object.ts:115 and read by the
// resource facade at object.ts:187.
func TestResourceKeyIsTheDeclaredName(t *testing.T) {
	var resource Resource[any] = newResourceImpl(nil, "db", ResourceDeclaration[any]{}, nil)
	if got := resource.Key(); got != "db" {
		t.Fatalf("Key() = %q", got)
	}
}

// packages/chord/src/cell.ts:89 (Source.validate): dev mode checks a write against the schema; production skips it.
func TestSourceValidateChecksSchemaInDevMode(t *testing.T) {
	dev, err := NewCell[any]("n", Schema{"type": "number"}, 0.0, allowWrites, SourceOptions{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := dev.Validate(1.0); err != nil {
		t.Fatalf("valid value: %v", err)
	}
	if err := dev.Validate("x"); err == nil {
		t.Fatal("dev mode accepted a string for a number schema")
	}
	prod, err := NewCell[any]("n", Schema{"type": "number"}, 0.0, allowWrites, SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := prod.Validate("x"); err != nil {
		t.Fatalf("production mode validated: %v", err)
	}
}

// packages/chord/src/types.ts:59 (Shape.contract): the provider contract's fingerprint.
func TestShapeContractIsTheProviderFingerprint(t *testing.T) {
	host := CreateHost(HostOptions{Name: "session:S1", Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Install(greeterFacet("hi", false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := getGreeter(t, host, clusterGreeter, "S1")
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	shape := ref.Shape()
	if shape == nil || shape.Contract != clusterGreeter.Fingerprint {
		t.Fatalf("shape %+v, fingerprint %q", shape, clusterGreeter.Fingerprint)
	}
}

// packages/chord/src/host.ts:91 (FacetLoader), stored at host.ts:238 and called with the module refs at host.ts:314.
func TestFacetLoaderLoadsModuleRefs(t *testing.T) {
	var asked [][]string
	host := CreateHost(HostOptions{Name: "session:S1", Self: kindHost(),
		Loader: FacetLoader(func(modules []string) ([]Facet, error) {
			asked = append(asked, slices.Clone(modules))
			return []Facet{greeterFacet("hi", false)}, nil
		}),
		OnError: func(error) {},
	})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load([]PluginRef{ModuleRef("./greeter.ts")}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []string{"./greeter.ts"}) {
		t.Fatalf("loader calls %v", asked)
	}
	if err := readyErr(t, getGreeter(t, host, clusterGreeter, "S1")); err != nil {
		t.Fatalf("loaded facet not served: %v", err)
	}
}

// packages/chord/src/reload.ts:256 adds every current service the new set lacks to `removed`, and reload.ts:594
// reports it (`removed: [...this.removed]`).
func TestReloadReportRemovedListsDroppedServices(t *testing.T) {
	host := CreateHost(HostOptions{Name: "server", Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Install(greeterFacet("hi", false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	report, err := host.Reload(nil, ReloadOptions{Remove: []string{"greeter"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.Removed, []string{clusterServerGreeter.Name}) {
		t.Fatalf("removed %v, want [%s]", report.Removed, clusterServerGreeter.Name)
	}
	if len(report.Changed) != 0 {
		t.Fatalf("changed %v", report.Changed)
	}
}
