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
)

var loopHolder = func() *contracts.Contract {
	contract, err := contracts.Local("p2.test.loop-holder", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("hold", contracts.NewMethod())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// Durable's scheduler reads the registry's Snapshot under its own lock, while a commit's publication runs in a section of the
// host's loop and waits for that lock: a Snapshot that waited for the loop (a registry value is read as a section of it)
// deadlocked the two (a worker with subagents under load stopped answering). The Snapshot answers while a section holds the
// loop: here the section is a method handler, which holds the loop until it returns.
func TestDurableRegistrySnapshotAnswersWhileASectionHoldsTheLoop(t *testing.T) {
	inSection, release := make(chan struct{}), make(chan struct{})
	holding := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(loopHolder, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				// A handler runs in the section that admitted its call, and holds the host's loop until it returns or suspends.
				"hold": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					close(inSection)
					<-release
					return nil, nil
				},
			}}}, nil
		}, host.ServeOptions{})
	})
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Worker: true, Facets: []host.Facet{p2test.WorkerFacet(&p2runtime.HarnessParts{}, nil, nil), holding}})
	extensions, err := fixture.Host.Registry(p2test.Background, p2runtime.Extensions.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	switches, err := fixture.Host.Registry(p2test.Background, p2runtime.ExtensionSwitches.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	registry := p2runtime.DurableRegistry(extensions, switches)
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
	answered := make(chan struct{})
	go func() {
		registry.Snapshot()
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Error("Snapshot waited for the host's loop")
	}
}
