//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// settleTimerMS is the timer of the agent_settled handler in TestRPCInputEndSettleTailTimerWithAHangingShutdownComparedWithPi.
const settleTimerMS = 4000

// D649, the timer cells of claim 1. RPC stdin ends while an agent_settled handler waits on a timer and another extension's quit session_shutdown handler never settles, so the
// dispose cannot return. Pi 1.5.2 stays alive while its event loop holds the handler's timer (3010 to 3020 ms for a 3 s timer). A Node handler holds pig the same way, as its
// runtime reports its own loop. A Go SDK handler on the same timer, with and without watching its context, does not hold pig: it exits 0 at once (15 to 40 ms).
func TestRPCInputEndSettleTailTimerWithAHangingShutdownComparedWithPi(t *testing.T) {
	timer := "RPC_SHUTDOWN_SETTLED_WAIT=4000"
	block := "RPC_SHUTDOWN_BLOCK_QUIT=1"
	legs := []struct {
		name  string
		start func(t *testing.T) *rpcProcess
		holds bool
	}{
		{"pi", func(t *testing.T) *rpcProcess { p, _ := startPiRPCShutdownFixture(t, block, timer); return p }, true},
		{"pig-node", func(t *testing.T) *rpcProcess { p, _ := startRPCShutdownFixture(t, block, timer); return p }, true},
		{"pig-go", func(t *testing.T) *rpcProcess {
			return startRPCShutdownGoFixtureWith(t, block, "RPC_SHUTDOWN_GO_SETTLED_TIMER_MS=4000", "RPC_SHUTDOWN_GO_SETTLED_MARK="+filepath.Join(t.TempDir(), "mark"))
		}, false},
		{"pig-go-watching", func(t *testing.T) *rpcProcess {
			return startRPCShutdownGoFixtureWith(t, block, "RPC_SHUTDOWN_GO_SETTLED_TIMER_MS=4000", "RPC_SHUTDOWN_GO_SETTLED_WATCH=1", "RPC_SHUTDOWN_GO_SETTLED_MARK="+filepath.Join(t.TempDir(), "mark"))
		}, false},
	}
	for _, leg := range legs {
		t.Run(leg.name, func(t *testing.T) {
			t.Parallel()
			p := leg.start(t)
			awaitReady(p)
			p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": "hello"})
			p.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
			// A Go handler is in flight before stdin ends: its marker is written when it starts.
			for _, env := range p.cmd.Env {
				if mark, ok := strings.CutPrefix(env, "RPC_SHUTDOWN_GO_SETTLED_MARK="); ok {
					for i := 0; ; i++ {
						if _, err := os.Stat(mark); err == nil {
							break
						}
						if i > 500 {
							t.Fatal("the Go agent_settled handler never started")
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
			}
			p.closeInput()
			ended := time.Now()
			drainRPCOutput(p)
			status := waitForRPCTermination(t, p)
			elapsed := time.Since(ended)
			if !status.Exited() || status.ExitStatus() != 0 {
				t.Fatalf("wait status = %v, want exit 0\n%s", status, p.stderr.String())
			}
			// The timer started at agent_settled, before stdin's end: a holding process lasts most of it; one that does not hold exits in a fraction.
			const holdsAtLeast, freeBelow = settleTimerMS * 6 / 10 * time.Millisecond, settleTimerMS * 4 / 10 * time.Millisecond
			if leg.holds && elapsed < holdsAtLeast {
				t.Fatalf("exited %s after stdin ended, want it held by the %d ms timer (at least %s)", elapsed, settleTimerMS, holdsAtLeast)
			}
			if !leg.holds && elapsed >= freeBelow {
				t.Fatalf("exited %s after stdin ended, want it not held by the %d ms timer (under %s)", elapsed, settleTimerMS, freeBelow)
			}
		})
	}
}
