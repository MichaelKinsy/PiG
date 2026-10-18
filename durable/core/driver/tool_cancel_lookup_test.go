// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// The core cancels a tool effect when its task is aborted, and it settles the call on the effect's completion, so a
// cancelled tool still reports tool_done (PROTOCOL.md, host rules; TestCancelledToolStillPostsItsCompletion). The cancel
// can arrive while the host is still resolving the tool among the conversation's agent's tools, before Execute runs: the
// call then ends there, and its completion must still reach the core. A tool that ends without one leaves its task
// running for good (CI 39123498272: pi.tool task 13 running with abortRequested, its generation waiting on it).
func TestToolCancelledWhileItsRegistrationResolvesStillPostsItsCompletion(t *testing.T) {
	resolving := make(chan struct{})
	core := &funcCore{respond: func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventAbort {
			return &abi.Step{Effects: []abi.Effect{{ID: 2, Kind: abi.EffectCancel, Payload: abi.U32Payload(1)}}}
		}
		return openWith("t", `{}`, "")(ev)
	}}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &driver.Tools{
		Lookup: func(ctx context.Context, _ durable.ConversationId, _ string) (durable.ToolRegistration, bool, error) {
			// The agent resolution waits on the effect's context, as coreSession.agent does.
			close(resolving)
			<-ctx.Done()
			return durable.ToolRegistration{}, false, ctx.Err()
		},
		Backend: func(driver.ToolCall) driver.ToolBackend { return noBackend{} },
	}
	host := sqlhost.New(sqlhost.Options{
		Core: core, DB: db, OwnDB: true, Clock: func() float64 { return 5000 },
		Handlers: map[abi.EffectKind]sqlhost.Handler{abi.EffectTool: runner.Handler()},
	})
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	runTool(t, core, host)
	<-resolving
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventAbort, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	// Every effect handler has returned, and a barrier event is processed behind whatever they posted.
	if err := host.WaitEffects(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := host.Send(t.Context(), abi.Event{Kind: abi.EventTimer}); err != nil {
		t.Fatal(err)
	}
	ev, ok := done(core)
	if !ok {
		t.Fatal("the tool effect ended with its registration lookup cancelled and posted no tool_done: the core never settles the call")
	}
	if ev.Phase != abi.OutcomeThrown || ev.ID != 1 {
		t.Fatalf("tool_done = outcome %d id %d %s, want the cancelled lookup as a thrown completion of effect 1", ev.Phase, ev.ID, ev.Payload)
	}
	if host.Err() != nil {
		t.Fatal(host.Err())
	}
}
