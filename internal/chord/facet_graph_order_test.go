package chord

import (
	"context"
	"testing"
)

type graphStep func(env *FacetEnvironment) error

func graphFacet(id string, steps ...graphStep) Facet {
	return DefineFacet(Facet{Id: id, Setup: func(env *FacetEnvironment) error {
		for _, step := range steps {
			if err := step(env); err != nil {
				return err
			}
		}
		return nil
	}})
}

// Pi packages/chord/src/facets/host.ts:353-356 (generation IDs), 807-877 (validateFacets, topologicalOrder): the order
// in which createFacetHost reports an invalid facet graph. Each expected message is what Pi's facet host reports for
// the same facets under Node.
func TestFacetGraphValidationMatchesPi(t *testing.T) {
	a, b, c := defineReader("graph.svc.a"), defineReader("graph.svc.b"), defineReader("graph.svc.c")
	impl := Reader(readerFunc(func(context.Context) (string, error) { return "", nil }))
	provide := func(def ServiceDefinition[Reader]) graphStep {
		return func(env *FacetEnvironment) error { return ProvideService(env, def, impl) }
	}
	many := func(def ServiceDefinition[Reader]) graphStep {
		return func(env *FacetEnvironment) error { _, err := ProvideMany(env, def); return err }
	}
	use := func(def ServiceDefinition[Reader]) graphStep {
		return func(env *FacetEnvironment) error { _, err := UseService(env, def); return err }
	}
	observe := func(def ServiceDefinition[Reader]) graphStep {
		return func(env *FacetEnvironment) error {
			return ObserveService(env, def, func(context.Context, Reader) error { return nil })
		}
	}
	for _, c := range []struct {
		name   string
		facets []Facet
		want   string
	}{
		{"dup provide", []Facet{graphFacet("a", provide(a)), graphFacet("b", provide(a))}, "Service graph.svc.a is provided by both a and b"},
		{"singleton+keyed", []Facet{graphFacet("a", provide(a)), graphFacet("b", many(a))}, "Service graph.svc.a is provided as both singleton and keyed"},
		{"keyed+singleton", []Facet{graphFacet("a", many(a)), graphFacet("b", provide(a))}, "Service graph.svc.a is provided as both singleton and keyed"},
		{"missing", []Facet{graphFacet("a", use(b))}, "Facet a requires local/graph.svc.b/singleton, but no facet provides it"},
		{"missing keyed", []Facet{graphFacet("a", observe(b))}, "Facet a requires local/graph.svc.b/keyed, but no facet provides it"},
		{"mode mismatch", []Facet{graphFacet("a", provide(a)), graphFacet("b", observe(a))}, "Facet b requires graph.svc.a as keyed, but a provides it as singleton"},
		{"mode mismatch2", []Facet{graphFacet("a", many(a)), graphFacet("b", use(a))}, "Facet b requires graph.svc.a as singleton, but a provides it as keyed"},
		{"cycle", []Facet{graphFacet("a", provide(a), use(b)), graphFacet("b", provide(b), use(a))}, "Facet dependency cycle: a, b"},
		{"cycle+downstream", []Facet{graphFacet("c", use(a)), graphFacet("a", provide(a), use(b)), graphFacet("b", provide(b), use(a))}, "Facet dependency cycle: c, a, b"},
		{"self use", []Facet{graphFacet("a", provide(a), use(a))}, ""},
		{"missing before dup", []Facet{graphFacet("a", use(c)), graphFacet("b", provide(a)), graphFacet("c", provide(a))}, "Service graph.svc.a is provided by both b and c"},
		{"empty id", []Facet{graphFacet("", provide(a))}, "Facet ID must not be empty"},
		{"dup ids", []Facet{graphFacet("a"), graphFacet("a")}, "Facet IDs must be unique within a generation"},
		{"empty+dup", []Facet{graphFacet("a"), graphFacet("a"), graphFacet("")}, "Facet ID must not be empty"},
		{"mismatch before missing", []Facet{graphFacet("a", use(c)), graphFacet("b", provide(a)), graphFacet("c", observe(a))}, "Facet a requires local/graph.svc.c/singleton, but no facet provides it"},
		{"both usage modes", []Facet{graphFacet("a", provide(a)), graphFacet("b", use(a), observe(a))}, "Facet b requires graph.svc.a as keyed, but a provides it as singleton"},
	} {
		host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: c.facets})
		got := ""
		if err != nil {
			got = err.Error()
		} else if err := host.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Pi packages/chord/src/facets/host.ts:424-430: reload checks every ID for emptiness, then uniqueness, then that each
// facet is active. Each expected message is what Pi's facet host reports for the same reload under Node.
func TestFacetReloadIDChecksMatchPi(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{[]string{"x", ""}, "Facet ID must not be empty"},
		{[]string{"x", "a", "a"}, "Reloaded facet IDs must be unique"},
		{[]string{"a", "a", ""}, "Facet ID must not be empty"},
		{[]string{"a", "x"}, "Facet x is not active"},
		{[]string{"b", "a"}, ""},
	} {
		host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{graphFacet("a"), graphFacet("b")}})
		if err != nil {
			t.Fatal(err)
		}
		facets := make([]Facet, len(c.ids))
		for i, id := range c.ids {
			facets[i] = graphFacet(id)
		}
		got := ""
		if err := host.Reload(t.Context(), facets); err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("reload %q: got %q, want %q", c.ids, got, c.want)
		}
		if err := host.Dispose(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
