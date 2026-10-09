package chord

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// bindOrderSource offers its ids as singletons and opens a RemoteServices whose Use logs the service and fails.
type bindOrderSource struct {
	ids []string
	log *[]string
}

func (*bindOrderSource) AcceptsUnavailableServices() bool { return false }

func (source *bindOrderSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	entries := make([]ServiceCatalogueEntry, len(source.ids))
	for i, id := range source.ids {
		entries[i] = ServiceCatalogueEntry{ServiceId: id, Mode: ServiceSingleton}
	}
	return entries, nil
}

func (source *bindOrderSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	ids := make([]string, len(options.Services))
	for i, service := range options.Services {
		ids[i] = service.Id()
	}
	*source.log = append(*source.log, "open "+strings.Join(ids, ","))
	return bindOrderServices{source.log}, nil
}

type bindOrderServices struct{ log *[]string }

func (services bindOrderServices) Use(serviceId string) (*RemoteService, error) {
	*services.log = append(*services.log, "use "+serviceId)
	return nil, errors.New("use " + serviceId + " failed")
}

func (bindOrderServices) Observe(string, func(context.Context, *RemoteService) error) (func(), error) {
	return func() {}, nil
}
func (bindOrderServices) Ready(context.Context) error   { return nil }
func (bindOrderServices) Dispose(context.Context) error { return nil }

// Pi packages/chord/src/facets/host.ts:687-713 (#bindServices) binds external services in the insertion order of the
// external Map, which #resolveExternalServices (host.ts:614-628) fills in first-requirement order. Measured on Pi 1.1.0 for a
// facet that uses srcbind.f, e, d, c, b, a from one source cataloguing them alphabetically: createFacetHost rejects with
// "use srcbind.f failed" and the source logs ["open srcbind.f,srcbind.e,srcbind.d,srcbind.c,srcbind.b,srcbind.a","use srcbind.f"].
func TestFacetHostBindsExternalServicesInRequirementOrderAsPi(t *testing.T) {
	ids := []string{"srcbind.f", "srcbind.e", "srcbind.d", "srcbind.c", "srcbind.b", "srcbind.a"}
	definitions := make([]ServiceDefinition[Reader], len(ids))
	for i, id := range ids {
		definitions[i] = defineReader(id)
	}
	facet := DefineFacet(Facet{Id: "x", Setup: func(env *FacetEnvironment) error {
		for _, definition := range definitions {
			if _, err := UseService(env, definition); err != nil {
				return err
			}
		}
		return nil
	}})
	for range 20 {
		var log []string
		source := &bindOrderSource{ids: []string{"srcbind.a", "srcbind.b", "srcbind.c", "srcbind.d", "srcbind.e", "srcbind.f"}, log: &log}
		_, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{facet}, ServiceSources: []RemoteServiceSource{source}})
		want := "open srcbind.f,srcbind.e,srcbind.d,srcbind.c,srcbind.b,srcbind.a | use srcbind.f"
		if err == nil || err.Error() != "use srcbind.f failed" || strings.Join(log, " | ") != want {
			t.Fatalf("err=%v log=%q, want Pi's use srcbind.f failed and %q", err, log, want)
		}
	}
}
