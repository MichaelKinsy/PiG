package cli

import (
	"context"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// Pi's prompt awaits command.handler (agent-session.ts:1766-1780) and writes the prompt response from preflightResult (rpc-mode.ts:394-413). A handler that settles without awaiting I/O has queued that response before the next input event, stdin end included (rpc-mode.ts:804-807), which shuts the process down. A subprocess transport's completion report therefore must not admit later input before the handler's result is published, while a blocked report admits it with the handler still pending.
func TestRPCAwaitHandlerAdmitsCompletedHandlerWithItsResult(t *testing.T) {
	for _, tc := range []struct {
		name      string
		report    func(context.Context)
		suspended bool
	}{
		{"completion", invocation.Complete, false},
		{"suspension", invocation.Acknowledge, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				turn := &rpcResponseTurn{write: func(any) {}}
				a := &rpcAdmission{ctx: t.Context(), turn: turn}
				release := make(chan struct{})
				admitted := make(chan *rpcPromise[int], 1)
				turn.begin()
				go func() {
					admitted <- rpcAwaitHandler(a, func(ctx context.Context) (int, error) {
						tc.report(ctx)
						<-release
						return 7, nil
					})
				}()
				synctest.Wait()
				var p *rpcPromise[int]
				select {
				case p = <-admitted:
					if !tc.suspended {
						t.Error("a completion report admitted later input before the handler's result was published")
					}
				default:
					if tc.suspended {
						t.Error("a suspended handler did not admit later input")
					}
				}
				close(release)
				if p == nil {
					p = <-admitted
				}
				var got []int
				p.then(func(v int, _ error) { got = append(got, v) })
				synctest.Wait()
				turn.end()
				if !slices.Equal(got, []int{7}) {
					t.Fatalf("continuation in the admitting input batch = %v, want [7]", got)
				}
			})
		})
	}
}

// Promise reactions are queued together, in registration order, before a reaction can add another reaction.
func TestRPCPromiseReactionOrder(t *testing.T) {
	var got []int
	turn := &rpcResponseTurn{write: func(any) {}}
	p := &rpcPromise[int]{turn: turn}
	p.then(func(v int, _ error) { got = append(got, v); p.then(func(v int, _ error) { got = append(got, v+2) }) })
	p.then(func(v int, _ error) { got = append(got, v+1) })
	p.resolve(1, nil)
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatal(got)
	}
	turn.begin()
	p.then(func(v int, _ error) { got = append(got, v+3) })
	if len(got) != 3 {
		t.Fatalf("fulfilled await ran inside the input callback: %v", got)
	}
	turn.end()
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatal(got)
	}
}

// Pi resumes pipelined `await session.setModel(model)` calls in the order the awaits began (rpc-mode.ts:470-478), whatever
// order their auth checks finish in, and after the responses of the lines handled before the drain.
func TestRPCAwaitInOrderResolvesInArrivalOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var got []any
		turn := &rpcResponseTurn{}
		turn.write = func(v any) { got = append(got, v) }
		a := &rpcAdmission{ctx: t.Context(), turn: turn}
		defer a.tasks.CloseAndWait()
		release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		start := make(chan struct{})
		close(start)
		var previous <-chan struct{} = start
		turn.begin()
		for i := range release {
			var p *rpcPromise[int]
			p, previous = rpcAwaitInOrder(a, previous, func() (int, error) {
				<-release[i]
				return i, nil
			})
			p.then(func(v int, _ error) { turn.write(v) })
		}
		turn.complete("get_state")
		for _, r := range slices.Backward(release) {
			close(r)
			synctest.Wait()
		}
		turn.end()
		synctest.Wait()
		if want := []any{"get_state", 0, 1, 2}; !reflect.DeepEqual(got, want) {
			t.Fatalf("responses=%v want=%v", got, want)
		}
	})
}

// Work that finishes before the next line of its batch is handled still resumes after that line's response: its result
// settles the promise from an event-loop task, which Node delivers only after the microtasks already queued.
func TestRPCAwaitInOrderEarlyWorkResumesAfterTheNextLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var got []any
		turn := &rpcResponseTurn{}
		turn.write = func(v any) { got = append(got, v) }
		a := &rpcAdmission{ctx: t.Context(), turn: turn}
		defer a.tasks.CloseAndWait()
		start := make(chan struct{})
		close(start)
		turn.begin()
		p, _ := rpcAwaitInOrder(a, start, func() (string, error) { return "set_model", nil })
		synctest.Wait()
		p.then(func(v string, _ error) { turn.write(v) })
		turn.complete("cycle_model")
		turn.end()
		if want := []any{"cycle_model", "set_model"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("responses=%v want=%v", got, want)
		}
	})
}

// Pi resumes an awaited operation that does real work (checkAuth, compaction checks, waiting for idle) from a later event-loop task, after the microtasks the current input callback queued, even when the work finished while the callback was still running. rpc-mode.ts:470-478 set_model awaits checkAuth, so a cycle_model later in the same input batch answers first (comb-audit row 21). The Go work runs on a goroutine; its completion must not take a queue position ahead of a later command's already fulfilled continuation because the goroutine happened to finish first.
func TestRPCAwaitWorkResumesAfterTheBatchsMicrotasks(t *testing.T) {
	for _, registerAfterDone := range []bool{false, true} {
		var got []string
		turn := &rpcResponseTurn{write: func(v any) { got = append(got, v.(string)) }}
		a := &rpcAdmission{ctx: t.Context(), turn: turn}
		turn.begin()
		done := make(chan struct{})
		work := rpcAwaitWork(a, true, func() (struct{}, error) { defer close(done); return struct{}{}, nil })
		if !registerAfterDone {
			work.then(func(struct{}, error) { turn.write("set") })
		}
		<-done
		// The goroutine has delivered its result before the later command of the batch runs: as an event-loop task, or, when the result bypasses the task queue, by settling the promise directly. Waiting for either keeps the bypass a deterministic ordering failure instead of a hang.
		delivered := func() bool {
			turn.mu.Lock()
			queued := len(turn.tasks) > 0
			turn.mu.Unlock()
			work.mu.Lock()
			defer work.mu.Unlock()
			return queued || work.done
		}
		for !delivered() {
			runtime.Gosched()
		}
		if registerAfterDone {
			work.then(func(struct{}, error) { turn.write("set") })
		}
		turn.complete("cycle")
		turn.end()
		a.taskGroup().CloseAndWait()
		if want := []string{"cycle", "set"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("registerAfterDone=%v: responses=%v want=%v", registerAfterDone, got, want)
		}
	}
}
