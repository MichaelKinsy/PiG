package sessions_test

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// A server restarted over a catalog that holds rows publishes them from the Catalog's native opener while the loop
// commits other work: the Writable it sets must be set in a section of the loop, as Pi's single loop runs the open
// continuation. Run with -race (the reviewer's probe, P2LeadF review F1).
func TestRestartOverAnExistingCatalogPublishesItsRowsWithoutRacingTheLoop(t *testing.T) {
	for range 10 {
		dir := t.TempDir()
		db, err := sessions.OpenCatalog(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Create(context.Background(), sessions.NewSession{Cwd: "/w", Title: "x", Model: durable.ModelRef{Provider: "p", ModelId: "m"}, ThinkingLevel: "high"}); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		f := newSessionsFixtureAt(t, dir)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				ref, err := f.Host.Get(p2test.Background, p2runtime.ServerLocal, delta.NewJsonObject(0), host.GetOptions{})
				if err == nil {
					_ = p2runtime.WhenLive(p2test.Background, ref)
					ref.Release()
				}
			}
		})
		catalog, err := f.Host.Get(p2test.Background, contract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p2runtime.WhenLive(p2test.Background, catalog); err != nil {
			t.Fatal(err)
		}
		rows, err := catalog.Member("sessions").Value()
		if rows, _ := rows.([]any); err != nil || len(rows) != 1 {
			t.Fatalf("catalog rows %v %v", rows, err)
		}
		catalog.Release()
		close(stop)
		wg.Wait()
	}
}
