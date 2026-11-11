package subagents

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports the Subagents cases of apps/p2/test/p2/contract-versions.test.ts (Pi main 335814952), and the guests' view of a
// failure (its reason, never its detail). The old contract is copied from the release that bundled it (6e11a00a0).
var subagentsV2 = func() *contracts.Contract {
	made, err := contracts.Remote("p2.subagents", contracts.RemoteDeclaration{
		Version:     2,
		Params:      contracts.Params{},
		HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("children", p2runtime.Record(schema.Arr(contract.ChildSchemaV2)))}}),
			contracts.Var("reporter", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
				contracts.Meth("report", contracts.NewMethod(p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("children", schema.Arr(contract.ChildSchemaV2))})))),
			}}),
		},
	})
	if err != nil {
		panic(err)
	}
	return made
}()

func decode(t *testing.T, text string) delta.JsonValue {
	t.Helper()
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func encode(t *testing.T, value delta.JsonValue) string {
	t.Helper()
	text, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func versionsFixture(t *testing.T) *p2test.TestHost {
	t.Helper()
	p2test.CheckLeaks(t, 0)
	dir := t.TempDir()
	info := p2test.Stub{Contract: p2runtime.ServerLocal, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{State: map[string]any{"info": delta.JsonObjectOf("app", "/app", "dataDir", dir, "fake", true, "allowedHosts", []any{}, "port", float64(1))}, Methods: inert("revokeOwners", "revokeGuests", "revokeFrames")}
	}}
	hosts := p2test.Stub{Contract: p2runtime.Hosts, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		workers := delta.JsonObjectOf("s1", delta.JsonObjectOf("running", true, "generation", float64(1)))
		return p2test.StubMembers{State: map[string]any{"workers": self.Cell(workers)}, Methods: inert("stop")}
	}}
	catalog := p2test.Stub{Contract: sessionscontract.Catalog, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{State: map[string]any{"sessions": self.Cell([]any{delta.JsonObjectOf("id", "s1", "env", nil)})}, Methods: inert("get", "create", "activity", "update")}
	}}
	return p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{serverFacet}, Stubs: []p2test.Stub{info, hosts, catalog}})
}

const currentChild = `{"name":"helper","conversationId":2,"model":"faux/faux-1","thinkingLevel":"off","tools":["read"],"lastEntryId":7,"updatedAt":5,"failure":{"reason":"model_error","detail":"/secret/path: overloaded"}}`
const releasedChild = `{"name":"helper","conversationId":2,"model":"faux/faux-1","thinkingLevel":"off","tools":["read"],"lastEntryId":7}`

func TestSubagentsV2ReaderSeesChildrenWithoutTimeAndFailureAndAV2WorkerReportsThem(t *testing.T) {
	f := versionsFixture(t)
	worker, err := p2test.HoldIn(f.Client(p2runtime.WorkerPrincipal{Session: "s1", Generation: 1}), contract.Subagents, delta.NewJsonObject(0), "reporter")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Release()
	owner := p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	current, err := p2test.HoldIn(f.Client(owner), contract.Subagents, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	if _, err := worker.Call("report", delta.JsonObjectOf("children", []any{decode(t, currentChild)}), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until := func(ref *host.Reference, want string) {
		t.Helper()
		if _, err := p2test.Until(ref.Member("children"), func(value delta.JsonValue) bool {
			all, _ := value.(*delta.JsonObject)
			return all != nil && encode(t, all.Value("s1")) == want
		}, 0); err != nil {
			t.Fatalf("children never were %s: %v", want, err)
		}
	}
	until(current, "["+currentChild+"]")
	old, err := p2test.HoldIn(f.Client(owner), subagentsV2, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	until(old, "["+releasedChild+"]")
	// A worker built then reports its children without the new fields; they read as absent.
	oldWorker, err := p2test.HoldIn(f.Client(p2runtime.WorkerPrincipal{Session: "s1", Generation: 1}), subagentsV2, delta.NewJsonObject(0), "reporter")
	if err != nil {
		t.Fatal(err)
	}
	defer oldWorker.Release()
	renamed := `{"name":"from-v2","conversationId":2,"model":"faux/faux-1","thinkingLevel":"off","tools":["read"],"lastEntryId":7}`
	if _, err := oldWorker.Call("report", delta.JsonObjectOf("children", []any{decode(t, renamed)}), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until(current, "["+renamed+"]")
}

func TestAGuestSeesAFailuresReasonNeverItsDetail(t *testing.T) {
	f := versionsFixture(t)
	worker, err := p2test.HoldIn(f.Client(p2runtime.WorkerPrincipal{Session: "s1", Generation: 1}), contract.Subagents, delta.NewJsonObject(0), "reporter")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Release()
	guest := p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestView}}
	reader, err := p2test.HoldIn(f.Client(guest), contract.Subagents, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Release()
	if _, err := worker.Call("report", delta.JsonObjectOf("children", []any{decode(t, currentChild)}), p2test.Background); err != nil {
		t.Fatal(err)
	}
	want := `[{"name":"helper","conversationId":2,"model":"faux/faux-1","thinkingLevel":"off","tools":["read"],"lastEntryId":7,"updatedAt":5,"failure":{"reason":"model_error"}}]`
	if _, err := p2test.Until(reader.Member("children"), func(value delta.JsonValue) bool {
		all, _ := value.(*delta.JsonObject)
		return all != nil && encode(t, all.Value("s1")) == want
	}, 0); err != nil {
		t.Fatalf("the guest's children never were %s: %v", want, err)
	}
}
