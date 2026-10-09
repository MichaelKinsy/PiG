package cli

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func gateOpen(g *rpcSettleGate) bool {
	select {
	case <-g.opened():
		return true
	default:
		return false
	}
}

// Pi emits agent_settled in microtasks after agent_end, and a stdin line is read only between macrotasks (agent-session.ts:1752-1777, 1044-1058; rpc-mode.ts:808-810). The gate is closed from agent_end until agent_settled, and open while the tail waits on something real: a compaction, an auto-retry delay or a new run.
func TestRPCSettleGateHoldsInputFromAgentEndToAgentSettled(t *testing.T) {
	g := &rpcSettleGate{}
	if !gateOpen(g) {
		t.Fatal("a gate that saw no agent_end held input")
	}
	g.before(agent.AgentEndEvent{})
	if gateOpen(g) {
		t.Fatal("input was admitted between agent_end and agent_settled")
	}
	g.after(agent.AgentEndEvent{})
	if gateOpen(g) {
		t.Fatal("writing agent_end opened the gate")
	}
	waiter := g.opened()
	g.before(agent.AgentSettledEvent{})
	select {
	case <-waiter:
		t.Fatal("the gate opened before agent_settled was written")
	default:
	}
	g.after(agent.AgentSettledEvent{})
	select {
	case <-waiter:
	default:
		t.Fatal("agent_settled did not release the held input")
	}
}

func TestRPCSettleGateOpensForRealWaitsAndNewRuns(t *testing.T) {
	cases := []struct {
		name          string
		begin, finish agent.AgentEvent
	}{
		{"compaction", agent.CompactionStartEvent{}, agent.CompactionEndEvent{}},
		{"auto-retry delay", agent.AutoRetryStartEvent{}, agent.AutoRetryEndEvent{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &rpcSettleGate{}
			g.before(agent.AgentEndEvent{})
			g.before(tc.begin)
			g.after(tc.begin)
			if !gateOpen(g) {
				t.Fatalf("input was held while a %s waits", tc.name)
			}
			g.before(tc.finish)
			g.after(tc.finish)
			if gateOpen(g) {
				t.Fatalf("the tail after the %s did not hold input again", tc.name)
			}
		})
	}
	t.Run("a continued run", func(t *testing.T) {
		g := &rpcSettleGate{}
		g.before(agent.AgentEndEvent{})
		g.after(agent.AgentStartEvent{})
		if !gateOpen(g) {
			t.Fatal("input was held while the continued run streams")
		}
	})
	t.Run("a manual compaction leaves no tail", func(t *testing.T) {
		g := &rpcSettleGate{}
		g.before(agent.CompactionStartEvent{})
		g.after(agent.CompactionEndEvent{})
		if !gateOpen(g) {
			t.Fatal("a compaction outside a tail closed the gate")
		}
	})
	t.Run("a replaced session's tail", func(t *testing.T) {
		g := &rpcSettleGate{}
		g.before(agent.AgentEndEvent{})
		g.reset()
		if !gateOpen(g) {
			t.Fatal("reset left the replaced session's tail held")
		}
	})
}

// A dialog that only stdin can answer, shutdown and the end of the mode stop the wait, so the gate cannot deadlock the loop.
func TestRPCSettleGateWaitEndsForDialogShutdownAndContext(t *testing.T) {
	never := func() <-chan struct{} { return make(chan struct{}) }
	closed := func() <-chan struct{} { c := make(chan struct{}); close(c); return c }
	g := &rpcSettleGate{}
	g.before(agent.AgentEndEvent{})
	run := func(dialog func() <-chan struct{}, stop <-chan struct{}, ctx context.Context) {
		t.Helper()
		done := make(chan struct{})
		go func() { g.wait(ctx, dialog, stop); close(done) }()
		select {
		case <-done:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("wait did not end")
		}
	}
	run(closed, nil, context.Background())
	stop := make(chan struct{})
	close(stop)
	run(never, stop, context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run(never, nil, ctx)
}

// A settle-tail handler that waits on a timer or I/O lets Pi read stdin, so a host's report of a waiting handler opens the gate until its count returns to zero. Each host reports into its own slot, and a replacement forgets the slots of the hosts that served the replaced Session.
func TestRPCSettleGateOpensWhileATailHandlerWaits(t *testing.T) {
	g := &rpcSettleGate{}
	first, second := g.tailHandlerReporter(), g.tailHandlerReporter()
	g.before(agent.AgentEndEvent{})
	first(1)
	if !gateOpen(g) {
		t.Fatal("input was held while a tail handler waits on something real")
	}
	second(0)
	if !gateOpen(g) {
		t.Fatal("another host's zero count closed the gate while the first host's handler waits")
	}
	first(0)
	if gateOpen(g) {
		t.Fatal("the tail did not hold input again once the handler answered")
	}
	first(1)
	g.reset()
	g.before(agent.AgentEndEvent{})
	if gateOpen(g) {
		t.Fatal("a replaced host's count opened the next Session's tail")
	}
}
