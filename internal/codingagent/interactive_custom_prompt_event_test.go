package codingagent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:1934-1960 seeds custom-message runs directly through _runAgentPrompt, not prompt().
func TestCustomSeedDoesNotEmitBeforeAgentStart(t *testing.T) {
	m, ctx, cancel := newLifecycleMode(t)
	var before atomic.Int32
	settled := make(chan struct{})
	m.newRunner = inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
		"before_agent_start": {func(...any) (any, error) { before.Add(1); return nil, nil }},
		"agent_settled":      {func(...any) (any, error) { close(settled); return nil, nil }},
	}}}, t.TempDir())
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	defer func() { cancel(); <-loopDone }()
	onLoop(m, ctx, func() { m.runTurn(ctx, "", func(context.Context) ([]agent.AgentMessage, error) { return nil, nil }) })
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("custom run did not settle")
	}
	if before.Load() != 0 {
		t.Fatalf("custom seed emitted before_agent_start %d times", before.Load())
	}
}
