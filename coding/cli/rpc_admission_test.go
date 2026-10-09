package cli

import (
	"context"
	"reflect"
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
