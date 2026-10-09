package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

type nodeFacetObserverProbe struct {
	t          *testing.T
	generation *nodeFacetGeneration
	spawner    *chord.ServiceSpawner[keyedProbe]
	log        *chord.RemoteService
	reported   chan error
}

type nodeFacetObserverTally struct {
	Total   int `json:"total"`
	Results int `json:"results"`
	Signals int `json:"signals"`
}

// startNodeFacetObserverProbe loads a Node facet whose keyed observer handler is `handler` and
// returns the probe. The facet exposes a tally of the returned values and observation signals
// that the Node garbage collector still holds.
func startNodeFacetObserverProbe(t *testing.T, handler string) *nodeFacetObserverProbe {
	t.Helper()
	isolateExperimentalTest(t)
	root := t.TempDir()
	entry := filepath.Join(root, "session.ts")
	writeNodeFacetFile(t, entry, `import { defineService } from "@earendil-works/chord";
import v8 from "node:v8";
import vm from "node:vm";
v8.setFlagsFromString("--expose-gc");
const gc = vm.runInNewContext("gc");
const Probe = defineService("test.keyed-probe");
const Log = defineService("test.node-observe-tally");
const results = [];
const signals = [];
const track = (result, signal) => { results.push(new WeakRef(Object(result))); signals.push(new WeakRef(signal)); return result; };
let releaseHold;
const held = new Promise(resolve => { releaseHold = resolve; });
export default {id:"observer",aborted(context){ return context.abortSignal?.aborted === true; },hold(){ return held; },release(){ releaseHold(); },setup(env){
  env.provide(Log,{async read(){
    for (let i = 0; i < 3; i++) { gc(); await new Promise(resolve => setTimeout(resolve, 0)); }
    return { total: results.length, results: results.filter(r => r.deref() !== undefined).length, signals: signals.filter(r => r.deref() !== undefined).length };
  }});
  env.observe(Probe,(service, context)=>track(`+handler+`, context.abortSignal));
}};
`)
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "observer"}, Entries: []FacetEntrySource{{Name: "session", Source: entry}}, Outdir: filepath.Join(root, "bundle")})
	if err != nil {
		t.Fatal(err)
	}
	probe := &nodeFacetObserverProbe{t: t, reported: make(chan error, 16)}
	var once sync.Once
	loader := &nodeFacetLoader{manifestPath: result.ManifestPath, entry: "session", optionalSession: true, pluginAPI: true}
	loaded, err := loader.load(t.Context(), func(ctx context.Context, generation *nodeFacetGeneration, method string, raw json.RawMessage) (json.RawMessage, error) {
		once.Do(func() { probe.generation = generation })
		return dispatchNodeFacetHost(ctx, generation, method, raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disposeBundleFacets(t, loaded) })
	provider := chord.Facet{Id: "@test/probe-provider", Setup: func(env *chord.FacetEnvironment) error {
		var err error
		probe.spawner, err = chord.ProvideMany(env, keyedProbeDefinition)
		return err
	}}
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: append([]chord.Facet{provider}, loaded.Facets...), OnError: func(err error) { probe.reported <- err }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: chord.ServiceIDs("test.node-observe-tally"), Transport: chord.NewLoopbackTransport(host.Services())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	probe.log, err = binding.Use("test.node-observe-tally")
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	return probe
}

func (probe *nodeFacetObserverProbe) spawn(key string) func() {
	probe.t.Helper()
	state, err := chord.NewReplicatedState(&keyedProbeState{Value: key})
	if err != nil {
		probe.t.Fatal(err)
	}
	closeInstance, err := probe.spawner.Spawn(key, &keyedProbeService{state: state, replace: func(string) error { return nil }})
	if err != nil {
		probe.t.Fatal(err)
	}
	return closeInstance
}

func (probe *nodeFacetObserverProbe) tally() nodeFacetObserverTally {
	probe.t.Helper()
	tally, err := chord.CallResult[nodeFacetObserverTally](probe.t.Context(), probe.log, "read")
	if err != nil {
		probe.t.Fatal(err)
	}
	return tally
}

// hostRetained counts the observation Contexts the Go side of the generation still tracks.
func (probe *nodeFacetObserverProbe) hostRetained() int {
	probe.generation.mu.Lock()
	defer probe.generation.mu.Unlock()
	return len(probe.generation.contextOrigins) + len(probe.generation.contextIds) + len(probe.generation.contextStops)
}

// eventually polls a condition whose completion is delivered by asynchronous Context cancellation callbacks.
func eventually(t *testing.T, what string, condition func() bool, state func() string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not settle: %s", what, state())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// upstream: packages/chord/src/services/instances.ts:106-109,127-136. Each observation is a task
// removed when its entry settles or its Context is cancelled, so a long-lived generation holds
// nothing per finished observation: neither the Context nor the returned Promise.
func TestNodeFacetObserverReleasesFinishedObservations(t *testing.T) {
	for name, result := range map[string]string{
		"pending promise": `new Promise(() => {})`,
		"non-promise":     `{ done: true }`,
	} {
		t.Run(name, func(t *testing.T) {
			probe := startNodeFacetObserverProbe(t, result)
			const observations = 8
			probe.tally()
			baseline := probe.hostRetained()
			closers := make([]func(), observations)
			for i := range closers {
				closers[i] = probe.spawn(strings.Repeat("k", i+1))
			}
			if held := probe.hostRetained(); held <= baseline {
				t.Fatal("live observations hold no Context; the retention probe is not measuring anything")
			}
			if got := probe.tally(); got.Total != observations || got.Signals != observations {
				t.Fatalf("live tally = %+v, want %d live observation signals", got, observations)
			}
			for _, closeInstance := range closers {
				closeInstance()
			}
			eventually(t, "finished observations", func() bool {
				tally := probe.tally()
				return tally.Total == observations && tally.Results == 0 && tally.Signals == 0 && probe.hostRetained() == baseline
			}, func() string {
				return fmt.Sprintf("node %+v, host contexts %d (baseline %d)", probe.tally(), probe.hostRetained(), baseline)
			})
			select {
			case err := <-probe.reported:
				t.Fatalf("cancelled observation reported %v", err)
			default:
			}
		})
	}
	// An observation cancelled before its Promise is awaited must still release the result.
	t.Run("cancelled before await", func(t *testing.T) {
		probe := startNodeFacetObserverProbe(t, `new Promise(() => {})`)
		const observations = 40
		for i := range observations {
			probe.spawn(fmt.Sprint("k", i))()
		}
		eventually(t, "cancelled observations", func() bool {
			tally := probe.tally()
			return tally.Total == observations && tally.Results == 0
		}, func() string { return fmt.Sprintf("%+v", probe.tally()) })
	})
}

// upstream: packages/chord/src/services/instances.ts:127-136. The handler result goes through
// Promise.resolve(...).catch(report), so a thenable is assimilated and a synchronous throw is
// reported, while a rejection that follows cancellation is not.
func TestNodeFacetObserverReportsAssimilatedFailures(t *testing.T) {
	for name, handler := range map[string]string{
		"thenable rejection": `{ then(resolve, reject) { reject(new Error("thenable failed")); } }`,
		"promise rejection":  `Promise.reject(new Error("promise failed"))`,
		"synchronous throw":  `(() => { throw new Error("sync failed"); })()`,
	} {
		t.Run(name, func(t *testing.T) {
			probe := startNodeFacetObserverProbe(t, handler)
			probe.spawn("first")
			select {
			case err := <-probe.reported:
				want := strings.SplitN(name, " ", 2)[0] + " failed"
				if name == "synchronous throw" {
					want = "sync failed"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("reported %v, want %q", err, want)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("observer failure was not reported")
			}
		})
	}
	t.Run("rejection after cancellation", func(t *testing.T) {
		probe := startNodeFacetObserverProbe(t, `{ then(resolve, reject) { context.abortSignal.addEventListener("abort", () => reject(new Error("late failure"))); } }`)
		closeInstance := probe.spawn("first")
		closeInstance()
		eventually(t, "cancelled observation", func() bool { return probe.tally().Results == 0 }, func() string { return fmt.Sprintf("%+v", probe.tally()) })
		select {
		case err := <-probe.reported:
			t.Fatalf("cancelled observation reported %v", err)
		default:
		}
	})
}

// upstream: packages/chord/src/services/instances.ts:127-136 only observes Promise.resolve(handler(...));
// it never releases the returned value. A handler result that is also registered elsewhere
// (here a thenable deactivation callback) must stay callable after the observation settles.
func TestNodeFacetObserverKeepsSharedResultReferences(t *testing.T) {
	isolateExperimentalTest(t)
	root := t.TempDir()
	entry := filepath.Join(root, "session.ts")
	writeNodeFacetFile(t, entry, `import { defineService } from "@earendil-works/chord";
const Probe = defineService("test.keyed-probe");
let releaseHold;
const held = new Promise(resolve => { releaseHold = resolve; });
export default {id:"observer",aborted(context){ return context.abortSignal?.aborted === true; },hold(){ return held; },release(){ releaseHold(); },setup(env){
  const cleanup = Object.assign(() => {}, { then(_resolve, reject) { reject(new Error("observation settled")); } });
  env.onDeactivate(cleanup);
  env.observe(Probe, () => cleanup);
}};
`)
	result, err := BundleFacets(t.Context(), BundleFacetsOptions{Plugin: FacetBundlePlugin{Id: "observer"}, Entries: []FacetEntrySource{{Name: "session", Source: entry}}, Outdir: filepath.Join(root, "bundle")})
	if err != nil {
		t.Fatal(err)
	}
	loader := &nodeFacetLoader{manifestPath: result.ManifestPath, entry: "session", optionalSession: true, pluginAPI: true}
	loaded, err := loader.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disposeBundleFacets(t, loaded) })
	var spawner *chord.ServiceSpawner[keyedProbe]
	provider := chord.Facet{Id: "@test/probe-provider", Setup: func(env *chord.FacetEnvironment) error {
		var err error
		spawner, err = chord.ProvideMany(env, keyedProbeDefinition)
		return err
	}}
	reported := make(chan error, 8)
	host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: append([]chord.Facet{provider}, loaded.Facets...), OnError: func(err error) { reported <- err }})
	if err != nil {
		t.Fatal(err)
	}
	state, err := chord.NewReplicatedState(&keyedProbeState{Value: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spawner.Spawn("first", &keyedProbeService{state: state, replace: func(string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	// The rejection is reported after the observation's "await" settles and releases its reference.
	select {
	case err := <-reported:
		if err == nil || !strings.Contains(err.Error(), "observation settled") {
			t.Fatalf("reported %v, want the observation rejection", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("observation rejection was not reported")
	}
	if err := host.Dispose(context.Background()); err != nil {
		t.Fatalf("dispose after a settled observation: %v", err)
	}
}

type uncomparableNodeFacetContext struct {
	context.Context
	_ []int
}

// A Context whose dynamic type is not comparable is never used as a map key; its abort must not panic.
func TestNodeFacetContextAbortAcceptsUncomparableContext(t *testing.T) {
	probe := startNodeFacetObserverProbe(t, `undefined`)
	ctx, cancel := context.WithCancel(context.Background())
	value := probe.generation.contextValue(uncomparableNodeFacetContext{Context: ctx})
	cancel()
	eventually(t, "aborted Context", func() bool {
		probe.generation.mu.Lock()
		defer probe.generation.mu.Unlock()
		_, held := probe.generation.contextOrigins[value.Origin]
		return !held
	}, func() string { return "Context origin still registered" })
}

// nodeFunction returns the reference of a function property of the probe facet. The facet is the first Node value the driver owns.
func (probe *nodeFacetObserverProbe) nodeFunction(name string) nodeFacetValue {
	probe.t.Helper()
	var function nodeFacetValue
	if err := probe.generation.request(probe.t.Context(), true, map[string]any{"op": "get", "id": "1", "property": name}, &function); err != nil {
		probe.t.Fatal(err)
	}
	if function.Kind != "node" || !function.Callable {
		probe.t.Fatalf("facet reference 1 property %s = %+v, want a Node function", name, function)
	}
	return function
}

// abortedOrigin asks the Node driver whether a Context value decodes as aborted.
func (probe *nodeFacetObserverProbe) abortedOrigin(value nodeFacetValue) bool {
	probe.t.Helper()
	var result nodeFacetValue
	if err := probe.generation.request(probe.t.Context(), false, map[string]any{"op": "invoke", "id": probe.nodeFunction("aborted").Id, "args": []nodeFacetValue{value}}, &result); err != nil {
		probe.t.Fatal(err)
	}
	if result.Kind != "json" {
		probe.t.Fatalf("aborted probe returned %+v, want a JSON boolean", result)
	}
	return string(result.Value) == "true"
}

// liveContextIds snapshots the ids the generation registered for Contexts it sent to Node.
func (probe *nodeFacetObserverProbe) liveContextIds() map[string]bool {
	probe.generation.mu.Lock()
	defer probe.generation.mu.Unlock()
	ids := map[string]bool{}
	for id := range probe.generation.contextOrigins {
		ids[id] = true
	}
	return ids
}

// upstream: packages/chord/src/services/instances.ts:106-109. An observation's task and everything
// it holds disappear with its entry. The Node driver keeps a Context's abort record only while a
// request that carries the Context can still arrive, so a finished observation leaves no record:
// the same origin decodes as not aborted once the host has forgotten it.
func TestNodeFacetObserverForgetsAbortRecords(t *testing.T) {
	probe := startNodeFacetObserverProbe(t, `{ done: true }`)
	probe.tally()
	baseline := probe.liveContextIds()
	const observations = 8
	closers := make([]func(), observations)
	for i := range closers {
		closers[i] = probe.spawn(strings.Repeat("k", i+1))
	}
	probe.tally()
	observed := map[string]bool{}
	for id := range probe.liveContextIds() {
		if !baseline[id] {
			observed[id] = true
		}
	}
	if len(observed) < observations {
		t.Fatalf("live observations registered %d Contexts, want at least %d", len(observed), observations)
	}
	for _, closeInstance := range closers {
		closeInstance()
	}
	eventually(t, "finished observations", func() bool { return len(probe.liveContextIds()) == len(baseline) }, func() string {
		return fmt.Sprintf("%d Contexts registered (baseline %d)", len(probe.liveContextIds()), len(baseline))
	})
	// A cancelled observation Context was aborted in Node before it was forgotten, so a record
	// that outlives the observation makes the same origin decode as aborted. The host sends the
	// forget notification after its own bookkeeping, hence the wait.
	for id := range observed {
		eventually(t, "Node abort record of finished observation Context "+id, func() bool {
			return !probe.abortedOrigin(nodeFacetValue{Kind: "context", Origin: id, Cancellable: true})
		}, func() string { return "Node still holds an abort record" })
	}
}

func (probe *nodeFacetObserverProbe) recordState(id string) (present, delivered bool, uses int) {
	probe.generation.mu.Lock()
	defer probe.generation.mu.Unlock()
	record := probe.generation.contextRecords[id]
	if record == nil {
		return false, false, 0
	}
	return true, record.delivered, record.uses
}

// A request that carries a Context can reach Node after the Context's abort notification, so Node
// keeps the abort record until every such request has returned, and forgets it right after.
func TestNodeFacetContextAbortRecordOutlivesInFlightRequests(t *testing.T) {
	probe := startNodeFacetObserverProbe(t, `{ done: true }`)
	generation := probe.generation
	origin := func(t *testing.T) (context.Context, context.CancelFunc, nodeFacetValue) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		return ctx, cancel, generation.contextValue(ctx)
	}
	waitDelivered := func(t *testing.T, id string, wantPresent bool) {
		t.Helper()
		eventually(t, "abort delivery", func() bool {
			present, delivered, _ := probe.recordState(id)
			return present == wantPresent && (delivered || !present)
		}, func() string {
			present, delivered, uses := probe.recordState(id)
			return fmt.Sprint(present, delivered, uses)
		})
	}
	t.Run("a leased Context keeps its record until the lease ends", func(t *testing.T) {
		_, cancel, value := origin(t)
		release := leaseNodeFacetContexts([]nodeFacetValue{value})
		cancel()
		waitDelivered(t, value.Origin, true)
		if present, _, uses := probe.recordState(value.Origin); !present || uses != 1 {
			t.Fatalf("record present=%v uses=%d while leased, want present with one use", present, uses)
		}
		// A request encoded before the abort decodes the Context after the abort notification.
		late := nodeFacetValue{Kind: "context", Origin: value.Origin, Cancellable: true}
		if !probe.abortedOrigin(late) {
			t.Fatal("a request that reaches Node after the abort notification decodes the Context as not aborted")
		}
		release()
		waitDelivered(t, value.Origin, false)
		if probe.abortedOrigin(late) {
			t.Fatal("Node kept the abort record after the last request returned")
		}
	})
	t.Run("an in-flight request holds the lease until it returns", func(t *testing.T) {
		_, cancel, value := origin(t)
		hold, release := probe.nodeFunction("hold"), probe.nodeFunction("release")
		returned := make(chan error, 1)
		go func() {
			returned <- generation.request(t.Context(), false, map[string]any{"op": "invoke", "id": hold.Id, "args": []nodeFacetValue{value}}, nil)
		}()
		eventually(t, "request lease", func() bool { _, _, uses := probe.recordState(value.Origin); return uses == 1 }, func() string { return "no lease" })
		cancel()
		waitDelivered(t, value.Origin, true)
		if !probe.abortedOrigin(nodeFacetValue{Kind: "context", Origin: value.Origin, Cancellable: true}) {
			t.Fatal("Node forgot the abort record while a request carrying the Context was in flight")
		}
		if err := generation.request(t.Context(), true, map[string]any{"op": "invoke", "id": release.Id, "args": []nodeFacetValue{}}, nil); err != nil {
			t.Fatal(err)
		}
		if err := <-returned; err != nil {
			t.Fatal(err)
		}
		waitDelivered(t, value.Origin, false)
		if probe.abortedOrigin(nodeFacetValue{Kind: "context", Origin: value.Origin, Cancellable: true}) {
			t.Fatal("Node kept the abort record after the request returned")
		}
	})
	t.Run("a Context that is already done travels as aborted", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(errors.New("gone"))
		raw, err := json.Marshal(generation.contextValue(ctx))
		if err != nil {
			t.Fatal(err)
		}
		var wire nodeFacetValue
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if !wire.Aborted || wire.AbortReason != "gone" {
			t.Fatalf("done Context encoded as %s, want aborted with its cause", raw)
		}
		// Node needs no earlier notification, or record, to decode such a value as aborted.
		if !probe.abortedOrigin(nodeFacetValue{Kind: "context", Origin: "never-registered", Cancellable: true, Aborted: true, AbortReason: "gone"}) {
			t.Fatal("Node decoded an aborted Context value as not aborted")
		}
		if probe.abortedOrigin(nodeFacetValue{Kind: "context", Origin: "never-registered", Cancellable: true}) {
			t.Fatal("decoding an aborted Context value left an abort record in Node")
		}
	})
}
