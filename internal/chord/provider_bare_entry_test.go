package chord

import (
	"reflect"
	"testing"
)

// provider.ts RemoteServiceProvider constructor: an entry without a service wrapper, `{ id }`, is a singleton catalogue entry (`"service" in entry ? entry : { service: entry, mode: "singleton" }`).
func TestRemoteServiceProviderDefinitionWithoutModeIsASingleton(t *testing.T) {
	provider, err := NewRemoteServiceProvider(
		ServiceProviderDefinition{Id: "test.bare"},
		KeyedService(keyedCounterDefinition),
		ServiceProviderDefinition{Id: "test.explicit", Mode: ServiceSingleton},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []ServiceCatalogueEntry{
		{ServiceId: "test.bare", Mode: ServiceSingleton},
		{ServiceId: keyedCounterDefinition.Id(), Mode: ServiceKeyed},
		{ServiceId: "test.explicit", Mode: ServiceSingleton},
	}
	if got := provider.Catalogue(); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogue = %#v, want %#v", got, want)
	}
	if _, err := NewRemoteServiceProvider(ServiceProviderDefinition{Id: "test.bad", Mode: "other"}); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if _, err := NewRemoteServiceProvider(ServiceProviderDefinition{Id: "dup"}, ServiceProviderDefinition{Id: "dup", Mode: ServiceSingleton}); err == nil {
		t.Fatal("duplicate IDs were accepted")
	}
}
