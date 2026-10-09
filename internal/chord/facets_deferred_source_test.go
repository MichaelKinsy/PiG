package chord

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// deferredSource offers an empty catalogue and accepts unavailable services: it may provisionally own requirements no catalogue offers.
type deferredSource struct {
	accepts bool
	opened  *[]string
}

func (source *deferredSource) AcceptsUnavailableServices() bool { return source.accepts }
func (source *deferredSource) Catalogue(context.Context) ([]ServiceCatalogueEntry, error) {
	return nil, nil
}
func (source *deferredSource) Open(options RemoteServiceSourceOpenOptions) (RemoteServices, error) {
	*source.opened = append(*source.opened, serviceReferenceIDs(options.Services)...)
	return nil, errors.New("deferred source opened")
}

// Pi packages/chord/src/types.ts:310 RemoteServiceSource.acceptsUnavailableServices (consumed at packages/chord/src/facets/host.ts:621): exactly one accepting source owns an absent requirement provisionally; none leaves it unresolved; two are ambiguous.
func TestAcceptsUnavailableServicesDecidesProvisionalOwnership(t *testing.T) {
	consumer := DefineFacet(Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		_, err := UseService(env, leftValueDefinition)
		return err
	}})
	var opened []string
	var asSource RemoteServiceSource = &deferredSource{accepts: true, opened: &opened}
	if !asSource.AcceptsUnavailableServices() || (&deferredSource{}).AcceptsUnavailableServices() {
		t.Fatal("AcceptsUnavailableServices does not report the source setting")
	}
	_, err := CreateFacetHost(context.Background(), FacetOptions{Facets: []Facet{consumer}, ServiceSources: []RemoteServiceSource{&deferredSource{accepts: true, opened: &opened}}})
	if err == nil || !strings.Contains(err.Error(), "deferred source opened") || len(opened) != 1 || opened[0] != leftValueDefinition.Id() {
		t.Fatalf("one accepting source: err=%v opened=%v", err, opened)
	}

	opened = nil
	_, err = CreateFacetHost(context.Background(), FacetOptions{Facets: []Facet{consumer}, ServiceSources: []RemoteServiceSource{&deferredSource{accepts: false, opened: &opened}}})
	if len(opened) != 0 || (err != nil && strings.Contains(err.Error(), "deferred source opened")) {
		t.Fatalf("source that refuses unavailable services was opened: err=%v opened=%v", err, opened)
	}

	_, err = CreateFacetHost(context.Background(), FacetOptions{Facets: []Facet{consumer}, ServiceSources: []RemoteServiceSource{&deferredSource{accepts: true, opened: &opened}, &deferredSource{accepts: true, opened: &opened}}})
	if err == nil || err.Error() != "Facet host service "+leftValueDefinition.Id()+" has more than one deferred source" {
		t.Fatalf("two accepting sources: %v", err)
	}
}
