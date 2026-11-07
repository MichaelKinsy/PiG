package cli

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// TestRPCSettleHandlerLatencyProbe measures what D86's HandlerWaitQuantum is sized against: how long a handler that does not wait takes from agent_end to agent_settled in each SDK. It is a measurement, not a gate, and runs only when PIG_PROBE_SETTLE_LATENCY is set:
//
//	PIG_PROBE_SETTLE_LATENCY=1 go test ./coding/cli -run TestRPCSettleHandlerLatencyProbe -v
//
// The gap is read on the client, so it bounds the host-to-extension round trip from above when the client keeps up (idle runs). With load=true the client's own goroutine is starved and records queue up, which can only shorten the gap it sees; the load figures of D86 (the request-to-response time of the agent_settled request, at most 8.7 ms under twice as many busy loops as CPUs) were read inside Conn.requestWithOrder by a temporary
//
//	if env.Request != nil && env.Request.Event == "agent_settled" { start := time.Now(); defer func() { println("PROBE_RTT", c.name, time.Since(start).Microseconds()) }() }
//
// placed before the response loop, with the probe reading "PROBE_RTT" lines from pig's stderr.
func TestRPCSettleHandlerLatencyProbe(t *testing.T) {
	if os.Getenv("PIG_PROBE_SETTLE_LATENCY") == "" {
		t.Skip("a measurement: set PIG_PROBE_SETTLE_LATENCY=1")
	}
	for _, load := range []bool{false, true} {
		for _, impl := range rpcSettleImplementations(t) {
			t.Run(fmt.Sprintf("%s/load=%v", impl.name, load), func(t *testing.T) {
				p := impl.start(t)
				awaitReady(p)
				var stop atomic.Bool
				defer stop.Store(true)
				if load {
					for range runtime.NumCPU() * 2 {
						go func() {
							for !stop.Load() {
							}
						}()
					}
				}
				var gaps []time.Duration
				for i := range 150 {
					p.sendJSON(map[string]any{"id": fmt.Sprint("p", i), "type": "prompt", "message": "hello"})
					var endAt time.Time
					p.await("agent_settled", func(r rpcRecord) bool {
						if r["type"] == "agent_end" {
							endAt = time.Now()
						}
						return r["type"] == "agent_settled"
					})
					gaps = append(gaps, time.Since(endAt))
				}
				slices.Sort(gaps)
				t.Logf("PROBE %s load=%v n=%d p50=%v p90=%v p99=%v max=%v", impl.name, load, len(gaps), gaps[len(gaps)/2], gaps[len(gaps)*9/10], gaps[len(gaps)*99/100], gaps[len(gaps)-1])
				p.closeAndWait("done")
			})
		}
	}
}
