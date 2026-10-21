package subprocess

import (
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi runs the calls of concurrent requests (the handlers of overlapping events and commands) on one thread, so the call an extension sent first
// applies first. The host reads the calls in the order the extension sent them and puts each request's calls on a lane of its own, so the lane
// goroutines must start their first calls in read order. One processor makes the scheduler's choice deterministic: the goroutine a push starts
// last runs first (runnext), so an unordered lane set runs the last-read call first.
func TestCallLanesStartTheCallsOfConcurrentRequestsInReadOrder(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const rounds, requests = 24, 16
	for round := range rounds {
		lanes := newCallLanes()
		var mu sync.Mutex
		var started []int
		var done sync.WaitGroup
		for i := range requests {
			done.Add(1)
			lanes.push(fmt.Sprintf("request-%d", i), func() {
				defer done.Done()
				mu.Lock()
				started = append(started, i)
				mu.Unlock()
			})
		}
		done.Wait()
		want := make([]int, requests)
		for i := range want {
			want[i] = i
		}
		if !slices.Equal(started, want) {
			t.Fatalf("round %d: calls of %d requests started as %v, want read order %v", round, requests, started, want)
		}
	}
}

// A call that waits for a request the host sent the extension must not hold back that request's own calls, which the extension sent after
// it and which sit in another lane. A call queued behind it in its own lane waits for it, so it takes its place only when it reaches the
// head, and the later lane's call starts in between.
func TestALaneBlockedOnItsOwnCallDoesNotHoldBackALaterRequestsCall(t *testing.T) {
	lanes := newCallLanes()
	blockedStarted := make(chan struct{})
	release := make(chan struct{})
	nestedApplied := make(chan struct{})
	queuedApplied := make(chan struct{})
	lanes.push("request", func() {
		close(blockedStarted)
		<-release
	})
	lanes.push("request", func() { close(queuedApplied) })
	waitSignal(t, blockedStarted, "the first call to start")
	lanes.push("nested", func() { close(nestedApplied) })
	waitSignal(t, nestedApplied, "the nested request's call while the first call waits")
	select {
	case <-queuedApplied:
		t.Fatal("a call queued behind a running call of its lane applied before that call ended")
	default:
	}
	close(release)
	waitSignal(t, queuedApplied, "the queued call after the first call ended")
}

// Pi runs the tools of a parallel batch on one thread, so the host calls they make apply in program order: tool 1's two calls, then tool 2's two
// calls (1a-1: appendEntry, ui.notify, setStatus, setActiveTools, setSessionName, setLabel and sendMessage of eight parallel tools). The
// connection reads the calls in the order the extension sent them, and the calls of every tool_call request share one lane, so a goroutine per
// request does not pick the order.
func TestCallsOfParallelToolRequestsApplyInSendOrder(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const rounds, requests, perRequest = 100, 8, 2
	var mu sync.Mutex
	var applied []int
	roundDone := make(chan struct{}, 1)
	f := newSocketCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		var args struct{ Seq int }
		_ = json.Unmarshal(call.Args, &args)
		mu.Lock()
		applied = append(applied, args.Seq)
		if len(applied)%(requests*perRequest) == 0 {
			roundDone <- struct{}{}
		}
		mu.Unlock()
		return &CallResultPayload{}, nil
	})
	for round := range rounds {
		var tools sync.WaitGroup
		for i := range requests {
			tools.Go(func() {
				_, _ = f.conn.requestWithUpdates(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: MethodToolCall, Tool: "tool", ToolCallID: fmt.Sprintf("call-%d-%d", round, i)}}, func(json.RawMessage) {})
			})
		}
		ids := make([]string, requests)
		for range requests {
			request := <-f.requests
			var index int
			if _, err := fmt.Sscanf(request.Request.ToolCallID, "call-%d-%d", new(int), &index); err != nil {
				t.Fatal(err)
			}
			ids[index] = request.ID
		}
		for i, id := range ids {
			for k := range perRequest {
				f.call(fmt.Sprintf("call-%d-%d-%d", round, i, k), "ui.setStatus", id, map[string]int{"Seq": i*perRequest + k})
			}
		}
		waitSignal(t, roundDone, "a round of calls")
		mu.Lock()
		got := slices.Clone(applied[round*requests*perRequest:])
		mu.Unlock()
		for i, seq := range got {
			if seq != i {
				t.Fatalf("round %d: the calls of %d parallel tools applied as %v, want the order they were sent", round, requests, got)
			}
		}
		for _, id := range ids {
			f.send(Envelope{Type: MsgResponse, ID: id, Response: &ResponsePayload{Result: json.RawMessage(`{"content":[]}`)}})
		}
		tools.Wait()
	}
}

// A call that waits for a request the host sent the extension must not hold that request's own calls back, however many calls the extension sent
// before it saw the request: the later calls of the waiting call's lane queue behind it, so the nested request's call, which sits in a lane of its
// own, runs in between and the queued calls run after the waiting call ends.
func TestANestedRequestsCallRunsWhileTheCallItWaitsInHasCallsQueuedBehindIt(t *testing.T) {
	var mu sync.Mutex
	var applied []string
	nested := make(chan struct{})
	waiting := make(chan struct{})
	record := func(name string) {
		mu.Lock()
		applied = append(applied, name)
		mu.Unlock()
	}
	f := newCallOrderFixture(t, func(call *CallPayload) (*CallResultPayload, error) {
		var args struct{ Name string }
		_ = json.Unmarshal(call.Args, &args)
		switch args.Name {
		case "waits":
			close(waiting)
			waitSignal(t, nested, "the nested request's call while the first call waits")
			record(args.Name)
		case "nested":
			record(args.Name)
			close(nested)
		default:
			record(args.Name)
		}
		return &CallResultPayload{}, nil
	})
	f.conn.pendingMu.Lock()
	f.conn.pending["nested"] = make(chan *Envelope, 1)
	f.conn.pendingMu.Unlock()
	f.call("c1", "setActiveTools", "", map[string]string{"Name": "waits"})
	waitSignal(t, waiting, "the first call to start")
	f.call("c2", "ui.notify", "", map[string]string{"Name": "queued"})
	f.call("c3", "ui.notify", "nested", map[string]string{"Name": "nested"})
	f.result("c3")
	f.result("c2")
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"nested", "waits", "queued"}; !slices.Equal(applied, want) {
		t.Fatalf("calls applied as %v, want %v", applied, want)
	}
}

// Pi's tools of a parallel batch call onUpdate on one thread (agent-loop.ts:836-840), so the update a tool called first reaches the
// listeners first. The connection reads update frames in the order the extension sent them; the callbacks of different requests ran on
// lanes of their own, so the scheduler chose among them (1a-5).
func TestToolUpdatesOfConcurrentRequestsReachTheirSinksInFrameOrder(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const rounds, requests, updates = 200, 4, 3
	hostEnd, peer := socketPair(t)
	conn := NewConn("updates", hostEnd)
	conn.Start(t.Context())
	serveDispatch(conn)
	t.Cleanup(func() { _ = peer.Close(); _ = conn.Close("test done") })
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	managed := withConn(&managedExt{config: ExtConfig{Name: "updates"}, host: host}, conn)
	handler := host.makeToolExecuteFunc(managed, "updates")
	for round := range rounds {
		var mu sync.Mutex
		var reached []string
		var calls sync.WaitGroup
		for i := range requests {
			id := fmt.Sprintf("tool-%d", i)
			calls.Go(func() {
				_, _ = handler(t.Context(), id, json.RawMessage(`{}`), agent.ToolUpdateCallback(func(agent.AgentToolResult) {
					mu.Lock()
					reached = append(reached, id)
					mu.Unlock()
				}))
			})
		}
		requestIDs := map[string]string{}
		var toolOrder []string
		for range requests {
			request := readLivenessEnvelope(t, peer)
			requestIDs[request.Request.ToolCallID] = request.ID
			toolOrder = append(toolOrder, request.Request.ToolCallID)
		}
		var sent []string
		for range updates {
			for _, tool := range toolOrder {
				update, err := json.Marshal(ToolUpdatePayload{RequestID: requestIDs[tool], Result: json.RawMessage(`{"content":[]}`)})
				if err != nil {
					t.Fatal(err)
				}
				writeLivenessEnvelope(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyToolUpdate, Args: update}})
				sent = append(sent, tool)
			}
		}
		for _, tool := range toolOrder {
			writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: requestIDs[tool], Response: &ResponsePayload{Result: json.RawMessage(`{"content":[]}`)}})
		}
		calls.Wait()
		mu.Lock()
		got := slices.Clone(reached)
		mu.Unlock()
		if !slices.Equal(got, sent) {
			t.Fatalf("round %d: the updates of %d concurrent requests reached their sinks as %v, want the frame order %v", round, requests, got, sent)
		}
	}
}

// serveDispatch stands in for the host's dispatcher: it takes the frames conn reads off Incoming and releases the dispatch markers makeToolExecuteFunc waits for.
func serveDispatch(conn *Conn) {
	go func() {
		for env := range conn.Incoming() {
			if env.barrier != nil {
				env.barrier.release()
			}
		}
	}()
}
