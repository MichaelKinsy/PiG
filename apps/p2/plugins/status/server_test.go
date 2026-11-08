package status_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// retryWorkers is a session workers component that answers Retry with a canned state.
type retryWorkers struct {
	p2runtime.NoWorkers
	state p2runtime.WorkerState
	asked chan string
}

func (w *retryWorkers) Retry(_ context.Context, session string) (p2runtime.WorkerState, error) {
	w.asked <- session
	return w.state, nil
}

// serverFixture is the status plugin's server facet over a stub of `Hosts` whose `workers` the test drives.
type serverFixture struct {
	*p2test.TestHost
	workers *p2test.Cell
	retry   *retryWorkers
}

func newServerFixture(t *testing.T) *serverFixture {
	t.Helper()
	p2test.CheckLeaks(t, 0)
	fixture := &serverFixture{retry: &retryWorkers{asked: make(chan string, 4)}}
	hostsStub := p2test.Stub{Contract: p2runtime.Hosts, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		fixture.workers = self.Cell(delta.NewJsonObject(0))
		return p2test.StubMembers{
			State:   map[string]any{"workers": fixture.workers},
			Methods: map[string]p2test.StubMethod{"stop": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
		}
	}}
	fixture.TestHost = p2test.NewTestHost(t, p2test.TestHostOptions{
		Facets: []host.Facet{status.Server},
		Stubs:  []p2test.Stub{hostsStub},
		Server: &p2runtime.ServerParts{Workers: fixture.retry},
	})
	return fixture
}

func worker(running bool, generation int, failure string) *delta.JsonObject {
	state := delta.JsonObjectOf("running", running, "generation", float64(generation))
	if failure != "" {
		state.Set("error", failure)
	}
	return state
}

func workersOf(pairs ...any) *delta.JsonObject {
	out := delta.NewJsonObject(len(pairs) / 2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

var owner = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}

func statuses(t *testing.T, ref *host.Reference, predicate func(*delta.JsonObject) bool) *delta.JsonObject {
	t.Helper()
	value, err := p2test.Until(ref.Member("statuses"), func(value delta.JsonValue) bool {
		object, _ := value.(*delta.JsonObject)
		return object != nil && predicate(object)
	}, 0)
	if err != nil {
		t.Fatalf("statuses: %v", err)
	}
	return value.(*delta.JsonObject)
}

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

func report(tasks int, busy ...int) *delta.JsonObject {
	list := make([]any, len(busy))
	for i, each := range busy {
		list[i] = float64(each)
	}
	return delta.JsonObjectOf("tasks", float64(tasks), "busy", list)
}

func TestStatusesFollowWorkersAndTheirOwnGenerationsReports(t *testing.T) {
	fixture := newServerFixture(t)
	reader, err := p2test.HoldIn(fixture.Client(owner), contract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Release()

	// A stopped worker, one that failed to start, and one that runs without a report yet.
	if err := fixture.SetCell(fixture.workers, workersOf("stopped", worker(false, 1, ""), "failed", worker(false, 2, "boom"), "idle", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	got := statuses(t, reader, func(all *delta.JsonObject) bool { return all.Len() == 3 })
	if want := `{"running":false,"tasks":0,"busy":[]}`; marshal(t, got.Value("stopped")) != want {
		t.Fatalf("stopped: %s", marshal(t, got.Value("stopped")))
	}
	if want := `{"running":false,"tasks":0,"busy":[],"error":"boom"}`; marshal(t, got.Value("failed")) != want {
		t.Fatalf("failed: %s", marshal(t, got.Value("failed")))
	}
	if want := `{"running":true,"tasks":0,"busy":[]}`; marshal(t, got.Value("idle")) != want {
		t.Fatalf("idle: %s", marshal(t, got.Value("idle")))
	}

	// The worker's own report (its principal names its session and generation) shows, its busy list ascending.
	reporter, err := p2test.HoldIn(fixture.Client(p2runtime.WorkerPrincipal{Session: "idle", Generation: 1}), contract.SessionStatus, delta.NewJsonObject(0), "reporter")
	if err != nil {
		t.Fatal(err)
	}
	defer reporter.Release()
	usage := delta.JsonObjectOf("input", float64(3))
	withUsage := report(2, 3, 1)
	withUsage.Set("usage", usage)
	if _, err := reporter.Call("report", withUsage, p2test.Background); err != nil {
		t.Fatal(err)
	}
	got = statuses(t, reader, func(all *delta.JsonObject) bool { return object(all.Value("idle")).Value("tasks") == float64(2) })
	if want := `{"running":true,"tasks":2,"busy":[1,3],"usage":{"input":3}}`; marshal(t, got.Value("idle")) != want {
		t.Fatalf("idle after report: %s", marshal(t, got.Value("idle")))
	}

	// A report of an older generation (the worker restarted while it travelled) is dropped, and a new generation starts
	// from nothing again.
	if err := fixture.SetCell(fixture.workers, workersOf("stopped", worker(false, 1, ""), "failed", worker(false, 2, "boom"), "idle", worker(true, 2, ""))); err != nil {
		t.Fatal(err)
	}
	got = statuses(t, reader, func(all *delta.JsonObject) bool { return object(all.Value("idle")).Value("tasks") == float64(0) })
	if _, err := reporter.Call("report", report(5, 9), p2test.Background); err != nil {
		t.Fatal(err)
	}
	// Nothing changed for generation 2: ask again through a state that does change, then look.
	if err := fixture.SetCell(fixture.workers, workersOf("stopped", worker(false, 1, ""), "failed", worker(false, 2, "boom"), "idle", worker(true, 2, ""), "more", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	got = statuses(t, reader, func(all *delta.JsonObject) bool { return all.Has("more") })
	if want := `{"running":true,"tasks":0,"busy":[]}`; marshal(t, got.Value("idle")) != want {
		t.Fatalf("a stale report showed: %s", marshal(t, got.Value("idle")))
	}
}

func TestGuestsSeeTheirSessionsWithoutErrorText(t *testing.T) {
	fixture := newServerFixture(t)
	if err := fixture.SetCell(fixture.workers, workersOf("mine", worker(false, 1, "stack at /secret/path"), "theirs", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	guest := p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: "mine", Sessions: map[string]p2runtime.GuestMode{"mine": p2runtime.GuestView}}
	reader, err := p2test.HoldIn(fixture.Client(guest), contract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Release()
	got := statuses(t, reader, func(all *delta.JsonObject) bool { return all.Len() == 1 })
	if want := `{"running":false,"tasks":0,"busy":[]}`; marshal(t, got.Value("mine")) != want {
		t.Fatalf("guest saw %s", marshal(t, got.Value("mine")))
	}
}

func TestWorkerControlIsTheOwnersAndReportsWhyAStartFailed(t *testing.T) {
	fixture := newServerFixture(t)
	params := delta.JsonObjectOf("session", "s1")

	guest := p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestFull}}
	denied, err := fixture.Client(guest).Hold(p2test.Background, contract.WorkerControl, params, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Release()
	failure, err := p2test.FailureOf(denied)
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, failure, data.CodeForbidden)

	control, err := p2test.HoldIn(fixture.Client(owner), contract.WorkerControl, params, "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer control.Release()
	fixture.retry.state = p2runtime.WorkerState{Running: true, Generation: 2}
	if _, err := control.Call("restart", nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
	if got := <-fixture.retry.asked; got != "s1" {
		t.Fatalf("retried %q", got)
	}
	reason := "no space left"
	fixture.retry.state = p2runtime.WorkerState{Running: false, Generation: 3, Error: &reason}
	_, err = control.Call("restart", nil, p2test.Background)
	var plugin *data.PluginError
	if !asPlugin(err, &plugin) || plugin.Code != "worker_failed" || plugin.Message != reason {
		t.Fatalf("restart error = %v", err)
	}
}

func TestSessionMetricsCountDistinctViewersAndShowUsage(t *testing.T) {
	fixture := newServerFixture(t)
	if err := fixture.SetCell(fixture.workers, workersOf("s1", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	view := func(viewer string) *host.Reference {
		ref, err := p2test.HoldIn(fixture.Client(owner), contract.SessionMetrics, delta.JsonObjectOf("session", "s1", "viewer", viewer), "reader")
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	first, second := view("tab-1"), view("tab-1")
	defer first.Release()
	clients := func(want float64) {
		t.Helper()
		if _, err := p2test.Until(first.Member("clients"), func(value delta.JsonValue) bool { return value == want }, 0); err != nil {
			t.Fatalf("clients %v: %v", want, err)
		}
	}
	// Two references of one viewer are one client.
	clients(1)
	third := view("tab-2")
	clients(2)
	second.Release()
	clients(2)
	third.Release()
	clients(1)

	// The spend of the session's worker.
	reporter, err := p2test.HoldIn(fixture.Client(p2runtime.WorkerPrincipal{Session: "s1", Generation: 1}), contract.SessionStatus, delta.NewJsonObject(0), "reporter")
	if err != nil {
		t.Fatal(err)
	}
	defer reporter.Release()
	args := report(0)
	args.Set("usage", delta.JsonObjectOf("input", float64(7)))
	if _, err := reporter.Call("report", args, p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(first.Member("usage"), func(value delta.JsonValue) bool { return marshal(t, value) == `{"input":7}` }, 0); err != nil {
		t.Fatalf("usage: %v", err)
	}

	// Only those who may view the session look at it.
	guest := p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: "other", Sessions: map[string]p2runtime.GuestMode{"other": p2runtime.GuestView}}
	denied, err := fixture.Client(guest).Hold(p2test.Background, contract.SessionMetrics, delta.JsonObjectOf("session", "s1", "viewer", "x"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Release()
	failure, err := p2test.FailureOf(denied)
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, failure, data.CodeForbidden)
}

func TestWorkerPushesItsStatusToTheServersIndex(t *testing.T) {
	fixture := newServerFixture(t)
	if err := fixture.SetCell(fixture.workers, workersOf("s1", worker(true, 1, ""))); err != nil {
		t.Fatal(err)
	}
	opened := p2test.OpenHarness(t)
	parts := &p2runtime.WorkerParts{Liveness: p2runtime.NewLiveness()}
	client := fixture.WorkerHost("s1", 1, p2test.WorkerFacet(p2test.HarnessParts(opened), parts, nil), status.Worker)
	reader, err := p2test.HoldIn(fixture.Client(owner), contract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Release()
	report, err := client.Hold(p2test.Background, contract.StatusReport, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer report.Release()
	if err := p2runtime.WhenLive(p2test.Background, report); err != nil {
		t.Fatal(err)
	}
	// `push` answers once the server committed the report (a method that starts work awaits it).
	if _, err := report.Call("push", nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
	got := statuses(t, reader, func(all *delta.JsonObject) bool { return all.Has("s1") })
	if want := `{"running":true,"tasks":0,"busy":[]}`; marshal(t, got.Value("s1")) != want {
		t.Fatalf("s1 = %s", marshal(t, got.Value("s1")))
	}
}
