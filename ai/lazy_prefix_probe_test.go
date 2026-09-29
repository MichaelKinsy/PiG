package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
)

// lazyProbe replays ai/testdata/d82-w7/lazy-probe.mjs, which runs Pi's real lazy.ts and event-stream.ts under Node and prints the order of every user-visible step. The Go executor must reproduce that order exactly.
type lazyProbe struct {
	t        *testing.T
	executor *continuationExecutor
	ctx      context.Context
	model    *Model
	joined   sync.WaitGroup

	mu  sync.Mutex
	log []string
}

func (probe *lazyProbe) mark(label string) {
	probe.mu.Lock()
	probe.log = append(probe.log, label)
	probe.mu.Unlock()
}

func (probe *lazyProbe) launch(body func()) { probe.joined.Go(body) }

// queueMicrotask is a ready reaction.
func (probe *lazyProbe) microtask(label string) { probe.executor.post(func() { probe.mark(label) }) }

// awaitFirst is lazyStream(model, async () => { await null; ... }): lazy.ts:48 with the setup body in the first reaction.
func (probe *lazyProbe) awaitFirst(ctx context.Context, setup func(context.Context) (*AssistantMessageEventStream, error)) *AssistantMessageEventStream {
	return startLazyStream(ctx, probe.model, setup, probe.launch)
}

// synchronous is lazyStream(model, async () => { ...no await... }).
func (probe *lazyProbe) synchronous(ctx context.Context, setup func(context.Context) (*AssistantMessageEventStream, error)) *AssistantMessageEventStream {
	return startLazyStreamSync(ctx, probe.model, setup, probe.launch)
}

// chain is the probe's chain(n): n microtasks, each queued by the previous one.
func (probe *lazyProbe) chain(n, index int) {
	probe.executor.post(func() {
		probe.mark("c" + strconv.Itoa(index))
		if index+1 < n {
			probe.chain(n, index+1)
		}
	})
}

func lazyProbeMessage(stop StopReason) *AssistantMessage {
	return &AssistantMessage{Content: []AssistantContentBlock{}, API: "probe-api", Provider: "probe", Model: "probe-model", StopReason: stop}
}

// pusher mirrors the probe's pusher: a microtask chain that pushes text_delta events and then done.
func (probe *lazyProbe) pusher(steps int) *AssistantMessageEventStream {
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
		probe.executor.post(step)
	}
	probe.executor.post(step)
	return inner
}

// consume is the probe's async consume(): its synchronous part runs the first iterator step in the caller's tick.
func (probe *lazyProbe) consume(turn *continuationTurn, stream *AssistantMessageEventStream) {
	ctx := context.WithValue(probe.ctx, continuationTurnKey{}, turn)
	for event := range stream.Events(ctx) {
		switch event := event.(type) {
		case TextDeltaEvent:
			probe.mark("event:text_delta:" + event.Delta)
		case DoneEvent:
			probe.mark("event:done")
		case ErrorEvent:
			probe.mark("event:error:" + event.Error.ErrorMessage)
		default:
			probe.t.Errorf("unexpected event %T", event)
		}
	}
	probe.mark("consumer-done")
	result := awaitContinuation(turn, stream.resultContinuation(probe.executor))
	probe.mark("result:" + string(result.StopReason))
}

func lazyProbeScenarios() map[string]func(*lazyProbe, *continuationTurn) {
	return map[string]func(*lazyProbe, *continuationTurn){
		"sync-setup-order": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			p.microtask("m1")
			stream := p.synchronous(p.ctx, func(context.Context) (*AssistantMessageEventStream, error) {
				p.mark("setup")
				return p.pusher(2), nil
			})
			p.microtask("m2")
			p.executor.post(func() { p.microtask("m3") })
			p.mark("returned")
			p.consume(turn, stream)
		},
		"sync-prefix-then-await": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			p.microtask("m1")
			// A setup's prefix is caller code that runs before the awaiting remainder is handed to LazyStream.
			p.mark("prefix")
			stream := p.awaitFirst(p.ctx, func(context.Context) (*AssistantMessageEventStream, error) {
				p.mark("resumed")
				return p.pusher(2), nil
			})
			p.microtask("m2")
			p.mark("returned")
			p.consume(turn, stream)
		},
		"await-first-setup": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			p.microtask("m1")
			stream := p.awaitFirst(p.ctx, func(context.Context) (*AssistantMessageEventStream, error) {
				p.mark("setup")
				return p.pusher(2), nil
			})
			p.microtask("m2")
			p.mark("returned")
			p.consume(turn, stream)
		},
		"sync-setup-error": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			p.microtask("m1")
			stream := p.synchronous(p.ctx, func(context.Context) (*AssistantMessageEventStream, error) {
				p.mark("setup")
				return nil, errors.New("boom")
			})
			p.microtask("m2")
			p.microtask("m3")
			p.chain(8, 0)
			p.mark("returned")
			p.consume(turn, stream)
		},
		"await-first-setup-error": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			p.microtask("m1")
			stream := p.awaitFirst(p.ctx, func(context.Context) (*AssistantMessageEventStream, error) {
				p.mark("setup")
				return nil, errors.New("boom")
			})
			p.microtask("m2")
			p.microtask("m3")
			p.microtask("m4")
			p.chain(8, 0)
			p.mark("returned")
			p.consume(turn, stream)
		},
		"model-runtime-composer-api": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			api := func(ctx context.Context) *AssistantMessageEventStream {
				return p.awaitFirst(ctx, func(context.Context) (*AssistantMessageEventStream, error) {
					p.mark("api-setup")
					return p.pusher(3), nil
				})
			}
			composer := func(ctx context.Context) *AssistantMessageEventStream {
				return p.synchronous(ctx, func(ctx context.Context) (*AssistantMessageEventStream, error) {
					p.mark("composer-setup")
					return api(ctx), nil
				})
			}
			stream := p.awaitFirst(p.ctx, func(ctx context.Context) (*AssistantMessageEventStream, error) {
				p.mark("runtime-setup")
				return composer(ctx), nil
			})
			p.microtask("m1")
			p.mark("returned")
			p.consume(turn, stream)
		},
		"sync-setup-prepushed-source": func(p *lazyProbe, turn *continuationTurn) {
			p.mark("call")
			stream := p.synchronous(p.ctx, func(context.Context) (*AssistantMessageEventStream, error) {
				p.mark("setup")
				inner := NewAssistantMessageEventStream()
				_ = inner.Push(TextDeltaEvent{ContentIndex: 0, Delta: "early", Partial: lazyProbeMessage(StopReasonPending)})
				p.mark("push:early")
				p.executor.post(func() {
					_ = inner.Push(DoneEvent{Reason: StopReasonStop, Message: lazyProbeMessage(StopReasonStop)})
					inner.End()
					p.mark("push:done")
				})
				return inner, nil
			})
			p.microtask("m1")
			p.microtask("m2")
			p.mark("returned")
			p.consume(turn, stream)
		},
	}
}

func runLazyProbeScenario(t *testing.T, scenario func(*lazyProbe, *continuationTurn)) []string {
	t.Helper()
	executor := &continuationExecutor{}
	probe := &lazyProbe{
		t:        t,
		executor: executor,
		ctx:      context.WithValue(t.Context(), continuationExecutorKey{}, executor),
		model:    &Model{ID: "probe-model", ProviderMeta: ProviderMetadata{API: "probe-api", ProviderID: "probe"}},
	}
	executor.run(func(turn *continuationTurn) {
		probe.ctx = context.WithValue(probe.ctx, continuationTurnKey{}, turn)
		scenario(probe, turn)
	})
	probe.joined.Wait()
	return probe.log
}

func lazyProbeGoldenPath() string { return filepath.Join("testdata", "d82-w7", "lazy-golden.json") }

func readLazyGolden(t *testing.T) map[string][]string {
	t.Helper()
	raw, err := os.ReadFile(lazyProbeGoldenPath())
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string][]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// The checked-in golden is Pi's own output. This test proves the golden is not stale by running the probe against the pinned source.
func TestLazyProbeGoldenMatchesPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Pi differential probe: %v", err)
	}
	out := filepath.Join(t.TempDir(), "lazy.json")
	packages, err := filepath.Abs(filepath.Join("..", ".upstream", "current", "packages"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), node, filepath.Join("testdata", "d82-w7", "lazy-probe.mjs"), packages, out)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("probe failed: %v\n%s", err, output)
	}
	live, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(lazyProbeGoldenPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(live) != string(golden) {
		t.Fatalf("lazy-golden.json is stale; regenerate it with the probe.\nlive:\n%s", live)
	}
}

// packages/ai/src/api/lazy.ts:48-61 and utils/event-stream.ts:44-91, measured on Node 24.19.0 and 26.7.0.
func TestLazyStreamMatchesPiMicrotaskOrder(t *testing.T) {
	golden := readLazyGolden(t)
	scenarios := lazyProbeScenarios()
	names := make([]string, 0, len(golden))
	for name := range golden {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(scenarios) != len(names) {
		t.Fatalf("Go replays %d scenarios, Pi probe printed %d", len(scenarios), len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			scenario, ok := scenarios[name]
			if !ok {
				t.Fatalf("no Go replay for probe scenario %q", name)
			}
			for run := range 20 {
				got := runLazyProbeScenario(t, scenario)
				if !slices.Equal(got, golden[name]) {
					t.Fatalf("run %d order differs from Pi\n got: %q\nwant: %q", run, got, golden[name])
				}
			}
		})
	}
}
