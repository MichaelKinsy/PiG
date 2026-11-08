package host

import (
	"context"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// A table read runs on a handler's goroutine, outside the host's sections, and checks its object is live: that check reads
// the state the object's end (a section) writes. Run with -race: the read must be a section of its own.
func TestAttemptTableReadsFromAHandlerGoroutineDoNotRaceWithTheObjectsEnd(t *testing.T) {
	// The seal lands among the reads only some of the time: many hosts make a race certain to show.
	for range 25 {
		tableReadsRaceWithTheObjectsEnd(t)
	}
}

func tableReadsRaceWithTheObjectsEnd(t *testing.T) {
	t.Helper()
	chordStore := memoryStore(t)
	rows, err := store.DefineTable("rows", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Int())}, Key: []string{"id"}})
	must(t, err)
	reader := mustContract(contracts.Local("app.reader", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("spin", contracts.NewMethod(contracts.Int()))},
	}))
	started := newSignal()
	finished := make(chan error, 1)
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(reader, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			handle := plain.(StoreSelf).Table(rows)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{"spin": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					started.fire()
					// Reads until the host's close aborts the call's context: the object's end (a section) runs meanwhile,
					// unordered with these reads unless the liveness check is a section.
					for ctx.Err() == nil {
						if _, err := handle.Get(background, jsonObject("id", 1.0)); err != nil {
							finished <- err
							return nil, nil
						}
					}
					finished <- nil
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	object := mustGet(t, host, reader, jsonObject())
	go func() { _, _ = refCall(object, "spin", delta.JsonValue(0.0)) }()
	started.wait(t)
	closeHosts(t, host)
	// A read after the object's end began is served, or refused `released`: neither is a race.
	if err := receive(t, finished); err != nil {
		wantChordError(t, err, data.CodeReleased, data.ReasonEnded, "app.reader: ended")
	}
}
