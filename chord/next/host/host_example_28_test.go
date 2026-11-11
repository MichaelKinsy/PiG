package host

// pi: packages/chord/test/examples/28-persisted-jobs.ts

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// --- 28-persisted-jobs.ts ------------------------------------------------------------------------------------------

func TestExample28PersistedJobs(t *testing.T) {
	importState := exDefineState("notes.import.state", documents.StateOptions{
		Schema:  exObj(exProp("done", exInt())),
		Initial: func() *delta.JsonObject { return jsonObject("done", float64(0)) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 16
		},
	})
	importJob, err := DefineJob("notes.import", JobOptions{
		Version: 1,
		Args:    exObj(exProp("path", exStr())),
		Result:  exObj(exProp("count", exInt())),
		State:   importState,
		// After a crash or a stop, `run` runs again: it must be idempotent.
		Persist: &JobPersistence{Resume: ResumeRerun},
	})
	must(t, err)
	notes := exLocal("notes.store", contracts.Params{},
		contracts.Mem("start", contracts.NewMethod(exStr(), exStr())), contracts.Mem("abort", contracts.NewMethod(exStr(), exStr())))
	// How the current process's `run` behaves: hang (to be cut off) or finish.
	var behaviour struct {
		sync.Mutex
		finish bool
	}
	var logs exCollector
	var jobs struct {
		sync.Mutex
		handle JobHandle
	}
	facet := func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			imports := self.Job(importJob, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					logs.add(fmt.Sprintf("run %s: from done = %v", exParam(args.(*delta.JsonObject), "path"), mustState(ctx).Value("done")))
					// Progress, deferred: published at once, stored within 100 ms (or with the next immediate change).
					deferred := documents.CommitPersistence{Kind: documents.CommitDeferred, WithinMs: new(100.0)}
					if _, err := ctx.State().Change(ctx, func(draft *delta.Object) (any, error) {
						return nil, draft.Set("done", draft.Get("done").(float64)+1)
					}, ChangeOptions{Persistence: &deferred}); err != nil {
						return nil, err
					}
					behaviour.Lock()
					finish := behaviour.finish
					behaviour.Unlock()
					if !finish {
						UntilAborted(ctx)
						return nil, context.Cause(ctx)
					}
					return jsonObject("count", mustState(ctx).Value("done")), nil
				},
				Finish: func(outcome Outcome, _ documents.Tx) error {
					if outcome.Kind == OutcomeDone {
						logs.add("finish: done " + exText(outcome.Result))
					} else {
						logs.add("finish: " + string(outcome.Kind) + " " + string(outcome.Error.Kind))
					}
					return nil
				},
			})
			jobs.Lock()
			jobs.handle = imports
			jobs.Unlock()
			return exImpl(nil, map[string]contracts.MethodFunction{
				"start": func(path delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					run, err := imports.Start(jsonObject("path", path), ctx, documents.StartOptions{})
					if err != nil {
						return nil, err
					}
					return run.ID(), nil
				},
				"abort": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					result, err := imports.Abort(id.(string), ctx)
					return string(result), err
				},
			}), nil
		}, ServeOptions{})
	}
	// process is a store over the file and a host serving `notes`.
	process := func(path string) (*store.ChordStore, Host) {
		st := exFileStore(t, path)
		closeTimeout := 100 * time.Millisecond
		host := newHost(t, HostOptions{Store: st, CloseTimeout: &closeTimeout, OnError: func(err HostError) { logs.add("onError: " + string(err.Kind)) }})
		mustLoad(t)(host.Load([]HostFacet{{Name: "@example/notes", Facet: DefineFacet(facet)}}, background))
		return st, host
	}
	path := t.TempDir() + "/session.sqlite"
	// 1. Start a run, then crash: the store closes under the live host.
	st, host := process(path)
	notesRef := exGet(t, host, notes, jsonObject())
	id := exCall(t, notesRef, "start", "inbox").(string)
	scopeEventually(t, "run 1", func() bool { return len(logs.all()) >= 1 })
	// The run's state: its base at the start, then the deferred change, stored by the object's timer within 100 ms.
	scopeEventually(t, "the deferred change was stored", func() bool { return len(stateRevisions(t, st)) >= 2 })
	exCloseStore(t, st)
	// 2. The next process counts the crash and reruns it (its state as stored: the deferred change was stored in time).
	st, host = process(path)
	loggedLen := -1
	scopeEventually(t, "the run is rerun", func() bool {
		if n := len(logs.all()); n != loggedLen {
			loggedLen = n
			t.Logf("log: %v", logs.all())
		}
		return slices_Contains(logs.all(), "run inbox: from done = 1")
	})
	scopeEventually(t, "the rerun's deferred change was stored", func() bool { return len(stateRevisions(t, st)) >= 3 })
	// 3. A clean close writes the run back `stopped`: the next process resumes it without counting.
	exCloseHost(t, host)
	exCloseStore(t, st)
	behaviour.Lock()
	behaviour.finish = true
	behaviour.Unlock()
	st, host = process(path)
	loggedFinish := -1
	scopeEventually(t, "the run finishes", func() bool {
		if n := len(logs.all()); n != loggedFinish {
			loggedFinish = n
			t.Logf("log: %v", logs.all())
		}
		return slices_Contains(logs.all(), `finish: done {"count":3}`)
	})
	// unsettled is `jobs.running.value.length`, read inside a section of the host.
	unsettled := func(on Host) int {
		jobs.Lock()
		handle := jobs.handle
		jobs.Unlock()
		count := 0
		on.(*chordHost).enter(func() {
			running, err := handle.Running().Value()
			must(t, err)
			count = len(running.([]any))
		})
		return count
	}
	scopeEventually(t, "no unsettled runs", func() bool { return unsettled(host) == 0 })
	// 4. A stored abort: marked, joined.
	behaviour.Lock()
	behaviour.finish = false
	behaviour.Unlock()
	notesRef = exGet(t, host, notes, jsonObject())
	second := exCall(t, notesRef, "start", "drafts").(string)
	scopeEventually(t, "the second run runs", func() bool { return slices_Contains(logs.all(), "run drafts: from done = 0") })
	exEqual(t, "abort", exCall(t, notesRef, "abort", second), "marked")
	scopeEventually(t, "the aborted run settled", func() bool { return unsettled(host) == 0 })
	exEqual(t, "once settled", exCall(t, notesRef, "abort", second), "terminal")
	exEqual(t, "the first run", exCall(t, notesRef, "abort", id), "terminal")
	exCloseHost(t, host)
	exCloseStore(t, st)
	want := []string{"run inbox: from done = 0", "run inbox: from done = 1", "run inbox: from done = 2", `finish: done {"count":3}`}
	got := []string{}
	for _, line := range logs.all() {
		if !strings.HasPrefix(line, "onError") {
			got = append(got, line)
		}
	}
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("log = %v, want prefix %v", got, want)
	}
}

// mustState is the run state's value (`ctx.state.value`).
func mustState(ctx JobCtx) *delta.JsonObject {
	value, err := ctx.State().Value()
	if err != nil {
		panic(err)
	}
	return value
}
