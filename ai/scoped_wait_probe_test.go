package ai

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// scopedWaitProbe replays ai/testdata/d82-w7/scoped-wait-probe.mjs. The consumer is agent-loop.ts:408-417 (for await + await emit) over agent.ts:605-607 (processEvents awaits each listener); the provider is a microtask chain that pushes into the stream.
type scopedWaitProbe struct {
	t        *testing.T
	executor *continuationExecutor
	log      []string
}

func (probe *scopedWaitProbe) mark(label string) { probe.log = append(probe.log, label) }

func (probe *scopedWaitProbe) chain(n, index int) {
	probe.executor.post(func() {
		probe.mark("c" + strconv.Itoa(index))
		if index+1 < n {
			probe.chain(n, index+1)
		}
	})
}

// provider mirrors the probe's provider(steps, ioAt): step ioAt-1 hands the next step to an I/O callback scheduled when the previous one ran.
func (probe *scopedWaitProbe) provider(steps, ioAt int) *AssistantMessageEventStream {
	inner := NewAssistantMessageEventStream()
	partial := lazyProbeMessage(StopReasonPending)
	index := 0
	var step func()
	step = func() {
		if index == steps {
			_ = inner.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
			inner.End()
			probe.mark("push:done")
			return
		}
		n := index
		index++
		_ = inner.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d" + strconv.Itoa(n), Partial: partial})
		probe.mark("push:" + strconv.Itoa(n))
		if n+1 == ioAt {
			probe.executor.postExternal(step)
		} else {
			probe.executor.post(step)
		}
	}
	if ioAt == 0 {
		probe.executor.postExternal(step)
	} else {
		probe.executor.post(step)
	}
	return inner
}

func scopedWaitLabel(event AssistantMessageEvent) string {
	if delta, ok := event.(TextDeltaEvent); ok {
		return delta.Delta
	}
	return string(event.EventType())
}

func (probe *scopedWaitProbe) run(steps, ioAt int, wait string) {
	executor := probe.executor
	executor.run(func(turn *continuationTurn) {
		ctx := context.WithValue(context.WithValue(probe.t.Context(), continuationExecutorKey{}, executor), continuationTurnKey{}, turn)
		stream := probe.provider(steps, ioAt)
		probe.chain(24, 0)
		for observation, event := range stream.ObserveEvents(ctx) {
			label := scopedWaitLabel(event)
			probe.mark("event:" + label)
			probe.mark("listener:" + label)
			switch wait {
			case "micro":
				observation.Yield()
			case "tick":
				_ = observation.AwaitTick(func() error { return nil })
			case "immediate":
				_ = observation.AwaitExternal(func() error { return nil })
			}
			probe.mark("listener-end:" + label)
			// emit's `await listener(event)` and the consumer's `await emit(event)` each resume one reaction later.
			observation.Yield()
			observation.Yield()
			probe.mark("emitted:" + label)
		}
		probe.mark("consumer-done")
	})
}

func scopedWaitGoldenPath() string {
	return filepath.Join("testdata", "d82-w7", "scoped-wait-golden.json")
}

func TestScopedWaitProbeGoldenMatchesPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Pi differential probe: %v", err)
	}
	out := filepath.Join(t.TempDir(), "scoped.json")
	packages, err := filepath.Abs(filepath.Join("..", ".upstream", "current", "packages"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, filepath.Join("testdata", "d82-w7", "scoped-wait-probe.mjs"), packages, out)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("probe failed: %v\n%s", err, output)
	}
	live, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(scopedWaitGoldenPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != string(golden) {
		t.Fatalf("scoped-wait-golden.json is stale; regenerate it with the probe.\nlive:\n%s", live)
	}
}

// A listener awaiting a promise reaction, a nextTick callback or an I/O callback lets the provider advance by a different amount. The scoped waits must match Node exactly (agent-loop.ts:417, agent.ts:605, output-guard.ts:95-101).
func TestScopedWaitsMatchPiOrder(t *testing.T) {
	raw, err := os.ReadFile(scopedWaitGoldenPath())
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string][]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	type shape struct {
		steps, ioAt int
		wait        string
	}
	shapes := map[string]shape{}
	for _, wait := range []string{"micro", "tick", "immediate"} {
		shapes["wait-"+wait] = shape{3, -1, wait}
		shapes["wait-"+wait+"-io-provider"] = shape{3, 1, wait}
	}
	if len(shapes) != len(golden) {
		t.Fatalf("Go replays %d scenarios, Pi probe printed %d", len(shapes), len(golden))
	}
	for name, want := range golden {
		t.Run(name, func(t *testing.T) {
			shape, ok := shapes[name]
			if !ok {
				t.Fatalf("no Go replay for probe scenario %q", name)
			}
			for run := range 20 {
				probe := &scopedWaitProbe{t: t, executor: &continuationExecutor{}}
				probe.run(shape.steps, shape.ioAt, shape.wait)
				if !slices.Equal(probe.log, want) {
					t.Fatalf("run %d order differs from Pi\n got: %q\nwant: %q", run, probe.log, want)
				}
			}
		})
	}
}

// A wait that ends while provider reactions are still running must resume after they finish, whatever the goroutine scheduling: Pi settles such a wait from a callback that runs only when the microtask queue is empty. Under a ready-queue resume the continuation lands inside the chain.
func TestAwaitExternalResumesAfterConcurrentReactionsDrain(t *testing.T) {
	const steps = 20000
	for run := range 20 {
		executor := &continuationExecutor{}
		var done atomic.Int64
		var chain func(int)
		chain = func(n int) {
			done.Add(1)
			if n+1 < steps {
				executor.post(func() { chain(n + 1) })
			}
		}
		started := make(chan struct{})
		var workers sync.WaitGroup
		executor.run(func(turn *continuationTurn) {
			observation := &StreamObservation{turn: turn}
			executor.mu.Lock()
			executor.observation = observation
			executor.mu.Unlock()
			defer observation.end()
			err := observation.AwaitExternal(func() error {
				// The response arrives while the provider's chain is running on another goroutine.
				workers.Go(func() { executor.post(func() { close(started); chain(0) }) })
				<-started
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := done.Load(); got != steps {
				t.Fatalf("run %d: continuation resumed after %d of %d provider reactions", run, got, steps)
			}
		})
		workers.Wait()
	}
}
