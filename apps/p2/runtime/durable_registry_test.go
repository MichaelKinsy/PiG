package runtime_test

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Durable's scheduler reads the registry's Snapshot under its own lock, while a commit's publication runs in a section of the
// host's loop and waits for that lock: a Snapshot that waited for the loop (a registry value is read as a section of it)
// deadlocked the two (a worker with subagents under load stopped answering). The Snapshot answers while a section holds the
// loop.
func TestDurableRegistrySnapshotAnswersWhileASectionHoldsTheLoop(t *testing.T) {
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Worker: true, Facets: []host.Facet{p2test.WorkerFacet(&p2runtime.HarnessParts{}, nil, nil)}})
	extensions, err := fixture.Host.Registry(p2test.Background, p2runtime.Extensions.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	switches, err := fixture.Host.Registry(p2test.Background, p2runtime.ExtensionSwitches.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	registry := p2runtime.DurableRegistry(extensions, switches)
	answered := make(chan struct{})
	if _, err := fixture.Root(func(host.Ctx) (any, error) {
		go func() {
			registry.Snapshot()
			close(answered)
		}()
		select {
		case <-answered:
		case <-time.After(5 * time.Second):
			t.Error("Snapshot waited for the host's loop")
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	<-answered
}
