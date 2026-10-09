package chord

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// orderSource offers ids as singletons and fails Open after logging which services it was opened for.
type orderSource struct {
	name    string
	ids     []string
	accepts bool
	log     *[]string
}

func (source *orderSource) AcceptsUnavailableServices() bool { return source.accepts }

func (source *orderSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	entries := make([]ServiceCatalogueEntry, len(source.ids))
	for i, id := range source.ids {
		entries[i] = ServiceCatalogueEntry{ServiceId: id, Mode: ServiceSingleton}
	}
	return entries, nil
}

func (source *orderSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	*source.log = append(*source.log, "open "+source.name+" "+strings.Join(serviceReferenceIDs(options.Services), ","))
	return nil, errors.New(source.name + " open failed")
}

// Pi packages/chord/src/facets/host.ts:596-648 (#resolveExternalServices): the host opens each source in the order of
// its first external requirement, as it groups requirements in a Map keyed by source. A service offered twice fails
// before any open, even when no facet requires it. Each expected error and open log is what Pi's facet host reports for
// the same facets and sources under Node.
func TestFacetHostOpensSourcesInRequirementOrderAsPi(t *testing.T) {
	a, b, c := defineReader("srcorder.a"), defineReader("srcorder.b"), defineReader("srcorder.c")
	user := func(id string, defs ...ServiceDefinition[Reader]) Facet {
		return DefineFacet(Facet{Id: id, Setup: func(env *FacetEnvironment) error {
			for _, def := range defs {
				if _, err := UseService(env, def); err != nil {
					return err
				}
			}
			return nil
		}})
	}
	var log []string
	src := func(name string, accepts bool, ids ...string) RemoteServiceSource {
		return &orderSource{name: name, ids: ids, accepts: accepts, log: &log}
	}
	for _, c := range []struct {
		name    string
		facets  []Facet
		sources []RemoteServiceSource
		want    string
		opens   string
	}{
		{"order", []Facet{user("x", a, b)}, []RemoteServiceSource{src("S1", false, "srcorder.b"), src("S2", false, "srcorder.a")}, "S2 open failed", "open S2 srcorder.a"},
		{"order2", []Facet{user("x", b), user("y", a)}, []RemoteServiceSource{src("S1", false, "srcorder.a"), src("S2", false, "srcorder.b")}, "S2 open failed", "open S2 srcorder.b"},
		{"deferred first", []Facet{user("x", c, a)}, []RemoteServiceSource{src("S1", false, "srcorder.a"), src("S2", true)}, "S2 open failed", "open S2 srcorder.c"},
		{"dup offer", []Facet{user("x", a)}, []RemoteServiceSource{src("S1", false, "srcorder.a", "srcorder.b"), src("S2", false, "srcorder.b", "srcorder.a")}, "Facet host service srcorder.b is offered by more than one source", ""},
		{"unused dup", []Facet{user("x", c)}, []RemoteServiceSource{src("S1", false, "srcorder.a"), src("S2", false, "srcorder.a")}, "Facet host service srcorder.a is offered by more than one source", ""},
		{"two deferred unused", []Facet{user("x", a)}, []RemoteServiceSource{src("S1", false, "srcorder.a"), src("S2", true), src("S3", true)}, "S1 open failed", "open S1 srcorder.a"},
	} {
		log = nil
		_, err := CreateFacetHost(t.Context(), FacetOptions{Facets: c.facets, ServiceSources: c.sources})
		if err == nil || err.Error() != c.want || strings.Join(log, " | ") != c.opens {
			t.Errorf("%s: err=%v opens=%q, want %q and %q", c.name, err, log, c.want, c.opens)
		}
	}
}

func serviceIdentityIds(services []ServiceReference) []string {
	ids := make([]string, len(services))
	for i, service := range services {
		ids[i] = service.Id()
	}
	return ids
}
