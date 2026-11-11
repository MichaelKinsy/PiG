package status_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/test/p2/status-step.test.ts (Pi main 335814952): what the status worker reports a busy conversation
// doing: its tool round's calls not done yet first, then a compaction, the model's answer, other work.

func slot(name string, status harness.ToolSlotStatus) harness.ToolSlot {
	return harness.ToolSlot{CallId: name, Name: name, Status: status}
}

func kinds(names ...string) map[string]bool {
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	return set
}

func TestStepOfNamesTheToolCallsOfTheRoundNotDoneYetInCallOrderBeforeAnythingElse(t *testing.T) {
	live := &harness.LiveState{Tools: []harness.ToolSlot{
		slot("read", harness.ToolSlotDone), slot("bash", harness.ToolSlotRunning), slot("edit", harness.ToolSlotPending), slot("sleep", "sleeping"),
	}}
	got := marshal(t, status.StepOf(live, kinds("pi.generation", "pi.compaction")))
	if want := `{"kind":"tools","names":["bash","edit","sleep"]}`; got != want {
		t.Fatalf("step = %s, want %s", got, want)
	}
}

func TestStepOfIsCompactingWithACompactionTaskOnceEveryCallIsDone(t *testing.T) {
	live := &harness.LiveState{Tools: []harness.ToolSlot{slot("read", harness.ToolSlotDone)}}
	if got, want := marshal(t, status.StepOf(live, kinds("pi.generation", "pi.compaction"))), `{"kind":"compacting"}`; got != want {
		t.Fatalf("step = %s, want %s", got, want)
	}
}

func TestStepOfIsThinkingWithAGenerationTaskAndNoCompaction(t *testing.T) {
	if got, want := marshal(t, status.StepOf(&harness.LiveState{}, kinds("pi.generation"))), `{"kind":"thinking"}`; got != want {
		t.Fatalf("step = %s, want %s", got, want)
	}
}

func TestStepOfIsWorkingWithOtherTasksOnlyOrWithoutALiveDocument(t *testing.T) {
	for _, live := range []*harness.LiveState{{}, nil} {
		if got, want := marshal(t, status.StepOf(live, kinds("p2.hook"))), `{"kind":"working"}`; got != want {
			t.Fatalf("step = %s, want %s", got, want)
		}
	}
	if got, want := marshal(t, status.StepOf(nil, kinds())), `{"kind":"working"}`; got != want {
		t.Fatalf("step = %s, want %s", got, want)
	}
}

// Ports the SessionStatus cases of test/p2/contract-versions.test.ts (Pi main 335814952). The old contract is copied
// verbatim from the release that bundled it (6e11a00a0), so its fingerprint is the one old bundles send.
var sessionStatusV1 = func() *contracts.Contract {
	statuses := p2runtime.Record(contract.StatusSchemaV1)
	contract, err := contracts.Remote("p2.session-status", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("statuses", statuses)}}),
			contracts.Var("reporter", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
				contracts.Meth("report", contracts.NewMethod(p2runtime.Contract(schema.Obj(schema.Properties{
					schema.Prop("tasks", schema.Num()),
					schema.Prop("busy", schema.Arr(schema.Num())),
					schema.Opt("usage", schema.Unknown()),
				})))),
			}}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

func TestSessionStatusV1ReaderSeesStatusesWithoutPerConversationWork(t *testing.T) {
	fixture := newServerFixture(t)
	if err := fixture.SetCell(fixture.workers, workersOf("s1", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	current, err := p2test.HoldIn(fixture.Client(owner), contract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	now := statuses(t, current, func(all *delta.JsonObject) bool { return object(all.Value("s1")) != nil })
	if !object(now.Value("s1")).Has("work") {
		t.Fatalf("the current status has no work: %s", marshal(t, now.Value("s1")))
	}
	old, err := p2test.HoldIn(fixture.Client(owner), sessionStatusV1, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	got := statuses(t, old, func(all *delta.JsonObject) bool { return object(all.Value("s1")) != nil })
	if want := `{"running":true,"tasks":0,"busy":[]}`; marshal(t, got.Value("s1")) != want {
		t.Fatalf("v1 status = %s, want %s", marshal(t, got.Value("s1")), want)
	}
}

func TestSessionStatusV1WorkerReportIsAcceptedWithNoConversationsWork(t *testing.T) {
	fixture := newServerFixture(t)
	if err := fixture.SetCell(fixture.workers, workersOf("s1", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	current, err := p2test.HoldIn(fixture.Client(owner), contract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	old, err := p2test.HoldIn(fixture.Client(p2runtime.WorkerPrincipal{Session: "s1", Generation: 1}), sessionStatusV1, delta.NewJsonObject(0), "reporter")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	if _, err := old.Call("report", delta.JsonObjectOf("tasks", float64(4), "busy", []any{float64(9), float64(2)}), p2test.Background); err != nil {
		t.Fatal(err)
	}
	got := statuses(t, current, func(all *delta.JsonObject) bool { return object(all.Value("s1")).Value("tasks") == float64(4) })
	// The field version 2 adds (each busy conversation's work) is empty.
	if want := `{"running":true,"tasks":4,"busy":[2,9],"work":{}}`; marshal(t, got.Value("s1")) != want {
		t.Fatalf("status after a v1 report = %s, want %s", marshal(t, got.Value("s1")), want)
	}
}
