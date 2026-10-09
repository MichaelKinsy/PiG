// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package chord

import "testing"

// TestProviderDefinitionTakesTheServiceMember: provider.ts:52 `{ service: { id, local? }, mode }` is a ServiceProviderDefinition whose
// Service names the service; the constructor (provider.ts:84) reads its id and local flag, so a local service is rejected. Pi's type
// requires mode; a Go definition without one is a singleton, the mode of the bare `{ id }` form. SingletonService and KeyedService
// build the `{ service, mode }` form, as Pi's callers do (server.ts:65, facets/host.ts:659).
func TestProviderDefinitionTakesTheServiceMember(t *testing.T) {
	t.Parallel()
	plain := DefineService[*FacetServiceImplementation]("psm.plain")
	keyed := DefineService[*FacetServiceImplementation]("psm.keyed")
	local := DefineService[*FacetServiceImplementation]("psm.local", ServiceOptions{Local: true})

	provider, err := NewRemoteServiceProvider(
		ServiceProviderDefinition{Service: plain},
		ServiceProviderDefinition{Service: keyed, Mode: ServiceKeyed},
		ServiceProviderDefinition{Id: "psm.bare"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []ServiceCatalogueEntry{{ServiceId: "psm.plain", Mode: ServiceSingleton}, {ServiceId: "psm.keyed", Mode: ServiceKeyed}, {ServiceId: "psm.bare", Mode: ServiceSingleton}}
	got := provider.Catalogue()
	if len(got) != len(want) {
		t.Fatalf("catalogue = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalogue[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	if got := SingletonService(keyed); got.Service == nil || got.Service.Id() != "psm.keyed" || got.Mode != ServiceSingleton {
		t.Fatalf("SingletonService is upstream's { service, mode: \"singleton\" }, got %+v", got)
	}
	if got := KeyedService(local); got.Service == nil || !got.Service.Local() || got.Mode != ServiceKeyed {
		t.Fatalf("KeyedService is upstream's { service, mode: \"keyed\" }, got %+v", got)
	}
	if _, err := NewRemoteServiceProvider(SingletonService(local)); err == nil || err.Error() != "Local service psm.local cannot be published remotely" {
		t.Fatalf("a local service passed through SingletonService is rejected, got %v", err)
	}
	if _, err := NewRemoteServiceProvider(ServiceProviderDefinition{Service: local}); err == nil || err.Error() != "Local service psm.local cannot be published remotely" {
		t.Fatalf("a local Service is rejected as upstream does, got %v", err)
	}
	if _, err := NewRemoteServiceProvider(ServiceProviderDefinition{Service: plain}, ServiceProviderDefinition{Id: "psm.plain"}); err == nil || err.Error() != "Remote service catalogue contains duplicate IDs" {
		t.Fatalf("the Service and ServiceId spellings of one id collide, got %v", err)
	}
}
