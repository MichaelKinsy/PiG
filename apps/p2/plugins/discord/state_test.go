package discord

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// The relay's routine reads the persisted state (Relay.Typing, the pending check) off the host's loop while a pending reply
// waits; closing the server seals the document in a section of the loop. The read is a section too, so the two cannot race.
// Run under -race.
func TestTheRoutineReadsTheStateWhileTheServerCloses(t *testing.T) {
	reader, err := contracts.Local("p2.discord-state-reader", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	reading := make(chan struct{})
	var once sync.Once
	facet := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(reader, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			doc := self.State(state).(*documents.Singleton)
			self.OnLive(func(ctx host.Ctx) *host.Handled {
				return host.AwaitingCtx(ctx, func() error {
					for ctx.Err() == nil {
						_ = docState{doc}.Value()
						once.Do(func() { close(reading) })
					}
					return nil
				})
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{}}, nil
		}))
	})
	store, err := p2runtime.OpenHostStore(p2test.Background, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(p2test.Background) })
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{facet}, Store: store})
	ref, err := fixture.Host.Hold(p2test.Background, reader, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	<-reading
	// Closing the host seals the document while the routine still reads it.
	fixture.Close()
	ref.Release()
}
