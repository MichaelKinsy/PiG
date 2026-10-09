package chord

import (
	"context"
	"slices"
	"testing"
)

// Pi packages/chord/src/facets/host.ts:306-320 (StagedServiceSpawner.spawn) stages the instance under its key before it calls the
// installer, and does not unstage it when the installer throws. After the host's provider is disposed, a spawn fails with the provider's
// error, and a second spawn under that key fails because the staged instance keeps the key. Instances spawned before the disposal still
// close. The trace is the Pi oracle's (/tmp/spawn1.mts, node over .upstream/current host.ts and provider.ts:183-212).
func TestFacetSpawnKeepsTheKeyWhenTheInstallerFailsAsPi(t *testing.T) {
	ctx := context.Background()
	var spawner *ServiceSpawner[Counter]
	host, err := CreateFacetHost(ctx, FacetOptions{Facets: []Facet{{Id: "f", Setup: func(env *FacetEnvironment) error {
		var provideErr error
		spawner, provideErr = ProvideMany(env, keyedCounterDefinition)
		return provideErr
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	var closeA func()
	try := func(label string, run func() error) {
		if err := run(); err != nil {
			log = append(log, label+" threw "+err.Error())
		} else {
			log = append(log, label+" ok")
		}
	}
	spawn := func(key string) func() error {
		return func() error {
			closeInstance, err := spawner.Spawn(key, newCounter(t))
			if err == nil && key == "a" {
				closeA = closeInstance
			}
			return err
		}
	}
	try("spawn a", spawn("a"))
	if err := host.Services().Dispose(); err != nil {
		t.Fatal(err)
	}
	try("spawn b", spawn("b"))
	try("spawn b again", spawn("b"))
	try("close a", func() error { closeA(); return nil })
	try("spawn a again", spawn("a"))
	try("host dispose", func() error { return host.Dispose(ctx) })
	want := []string{
		"spawn a ok",
		"spawn b threw Remote service provider is disposed",
		"spawn b again threw Facet service already has a live instance with key b",
		"close a ok",
		"spawn a again threw Remote service provider is disposed",
		"host dispose ok",
	}
	if !slices.Equal(log, want) {
		t.Errorf("trace differs from Pi:\n got %q\nwant %q", log, want)
	}
}
