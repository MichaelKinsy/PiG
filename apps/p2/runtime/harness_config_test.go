package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

var configSwap = func() *contracts.Contract {
	contract, err := contracts.Local("p2.test.config-swap", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("swap", contracts.NewMethod())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// The scheduler reads the Harness's settings under its own lock (the restart limit of a task cut off), while a batch's observer
// that wakes the scheduler (Durable's registry) holds the host's loop and waits for that lock: a read of the configuration
// that waited for the loop (a registry's value is read as a section of it) deadlocked the two, and a worker restarted after a
// kill never resumed its script. The view answers while a section holds the loop (here a method handler, which holds the loop
// until it returns), and follows the compositions of the batches that compose the configuration.
func TestHarnessConfigViewAnswersWhileASectionHoldsTheLoopAndFollowsTheBatchesThatCompose(t *testing.T) {
	inSection, release := make(chan struct{}), make(chan struct{})
	first, second := &harness.HarnessSettings{}, &harness.HarnessSettings{}
	var configured state.Writable[any]
	facet := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := p2runtime.Contribute(facet, "p2.test.config", func(self host.StoreSelf) error {
			configured = self.Writable(p2runtime.Keyed[p2runtime.HarnessConfigEntry]{
				p2runtime.Entry("default", p2runtime.HarnessConfigEntry{Settings: func() *harness.HarnessSettings { return first }}),
			})
			self.Contribute(p2runtime.HarnessConfig.Registry, configured)
			return nil
		}); err != nil {
			return err
		}
		if err := facet.Serve(configSwap, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"swap": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return nil, configured.Set(p2runtime.Keyed[p2runtime.HarnessConfigEntry]{
						p2runtime.Entry("default", p2runtime.HarnessConfigEntry{Settings: func() *harness.HarnessSettings { return second }}),
					})
				},
			}}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(loopHolder, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"hold": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					close(inSection)
					<-release
					return nil, nil
				},
			}}}, nil
		}, host.ServeOptions{})
	})
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Worker: true, Facets: []host.Facet{p2test.WorkerFacet(&p2runtime.HarnessParts{}, nil, nil), facet}})
	config, err := fixture.Host.Registry(p2test.Background, p2runtime.HarnessConfig.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := p2runtime.ObserveHarnessConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Stop()
	settingsOf := func() *harness.HarnessSettings {
		entry, ok := view.Default()
		if !ok {
			t.Fatal("the view has no default configuration")
		}
		return entry.Settings()
	}
	if settingsOf() != first {
		t.Fatal("the view does not start at the composition the registry had")
	}
	swapper, err := fixture.Host.Get(p2test.Background, configSwap, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := swapper.Call("swap", nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
	if settingsOf() != second {
		t.Fatal("the view did not follow the batch that composed the new configuration")
	}
	holder, err := fixture.Host.Get(p2test.Background, loopHolder, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		_, _ = holder.Call("hold", nil, p2test.Background)
	}()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-opened
	}()
	<-inSection
	answered := make(chan *harness.HarnessSettings, 1)
	go func() {
		entry, _ := view.Default()
		answered <- entry.Settings()
	}()
	select {
	case got := <-answered:
		if got != second {
			t.Error("the view answered another configuration than the last composed")
		}
	case <-time.After(5 * time.Second):
		t.Error("the configuration waited for the host's loop")
	}
}
