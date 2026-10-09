package chord

import "testing"

// Pi packages/chord/src/services/provider.ts:113-125 (provide), :143-151 (validateReplacement) and :154-160 (replace) run
// #assertActive, #assertRemotable, #assertAllowed and #registration(id, "singleton") (and, for provide, the existing
// provider check at :117-119) before classifyRemoteServiceImplementation. An implementation that cannot be published
// therefore loses to every one of those errors. The messages below are what Pi throws for the same calls under Node.
func TestProviderSingletonChecksPrecedeTheImplementation(t *testing.T) {
	newProvider := func(definitions ...ServiceProviderDefinition) *RemoteServiceProvider {
		t.Helper()
		provider, err := NewRemoteServiceProvider(definitions...)
		if err != nil {
			t.Fatal(err)
		}
		return provider
	}
	var invalid Counter // nil: "implementation must be an object" if classified
	single := func(t *testing.T) *RemoteServiceProvider {
		t.Helper()
		return newProvider(ServiceProviderDefinition{Id: counterDefinition.Id()})
	}
	type call struct {
		name string
		run  func(*RemoteServiceProvider) error
	}
	calls := []call{
		{"Provide", func(p *RemoteServiceProvider) error { return Provide(p, counterDefinition, invalid) }},
		{"Replace", func(p *RemoteServiceProvider) error { return Replace(p, counterDefinition, invalid) }},
		{"ValidateReplacement", func(p *RemoteServiceProvider) error { return ValidateReplacement(p, counterDefinition, invalid) }},
	}
	check := func(t *testing.T, label string, err error, want string) {
		t.Helper()
		if err == nil || err.Error() != want {
			t.Errorf("%s: error %v, want %q", label, err, want)
		}
	}
	for _, c := range calls {
		disposed := single(t)
		if err := disposed.Dispose(); err != nil {
			t.Fatal(err)
		}
		check(t, c.name+" on a disposed provider", c.run(disposed), "Remote service provider is disposed")

		other := newProvider(ServiceProviderDefinition{Id: "test.a"})
		check(t, c.name+" of a service not in the catalogue", c.run(other), "Remote service test.counter is not allowlisted")

		keyed := newProvider(KeyedService(counterDefinition))
		check(t, c.name+" of a keyed service", c.run(keyed), "Remote service test.counter is keyed, not singleton")
	}
	provided := single(t)
	if err := Provide(provided, counterDefinition, Counter(newCounter(t))); err != nil {
		t.Fatal(err)
	}
	check(t, "Provide twice", Provide(provided, counterDefinition, invalid), "Remote service test.counter already has a provider")
}
