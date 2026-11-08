package subagents

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func inert(names ...string) map[string]p2test.StubMethod {
	methods := map[string]p2test.StubMethod{}
	for _, name := range names {
		methods[name] = func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
	}
	return methods
}

// A server restarted over a child store that holds rows publishes them from the store's native opener while the loop
// commits other work: the Writable it sets must be set in a section of the loop, as Pi's single loop runs the open
// continuation. Run with -race (the review probe of the Catalog's opener, for this store).
func TestRestartOverAnExistingChildStorePublishesItsRowsWithoutRacingTheLoop(t *testing.T) {
	for range 10 {
		dir := t.TempDir()
		db, err := sql.Open("sqlite", filepath.Join(dir, "subagents.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		child := `[{"name":"a","conversationId":2,"model":"p/m","thinkingLevel":"high","tools":[],"lastEntryId":3,"cwd":"/w"}]`
		if _, err := db.Exec("CREATE TABLE children(session TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT; INSERT INTO children VALUES('s1', '" + child + "')"); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		p2test.CheckLeaks(t, 0)
		info := p2test.Stub{Contract: p2runtime.ServerLocal, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{State: map[string]any{"info": delta.JsonObjectOf("app", "/app", "dataDir", dir, "fake", true, "allowedHosts", []any{}, "port", float64(1))}, Methods: inert("revokeOwners", "revokeGuests")}
		}}
		hosts := p2test.Stub{Contract: p2runtime.Hosts, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{State: map[string]any{"workers": self.Cell(delta.NewJsonObject(0))}, Methods: inert("stop")}
		}}
		catalog := p2test.Stub{Contract: sessionscontract.Catalog, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{State: map[string]any{"sessions": self.Cell([]any{delta.JsonObjectOf("id", "s1", "env", nil)})}, Methods: inert("get", "create", "activity", "update")}
		}}
		f := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{serverFacet}, Stubs: []p2test.Stub{info, hosts, catalog}})
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
		store, err := f.Host.Get(p2test.Background, contract.ChildStore, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p2runtime.WhenLive(p2test.Background, store); err != nil {
			t.Fatal(err)
		}
		children, err := store.Member("children").Value()
		if err != nil {
			t.Fatal(err)
		}
		if rows, _ := object(children).Value("s1").([]any); len(rows) != 1 {
			t.Fatalf("children %v", children)
		}
		store.Release()
		close(stop)
		wg.Wait()
	}
}
