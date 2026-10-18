package session_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// nodeBehavior scripts one phase of a node task; nil keeps the default (harness-structured.test.ts Node).
type nodeBehavior struct {
	run, resume, abort func(rt *session.Runtime, done func(error))
}

type nodeGate struct {
	open    bool
	waiters []func()
}

// nodeWorld is the core's version of the Node task world of Pi harness-structured.test.ts (:59-180): a node named by its
// input runs its script's behavior, and by default its run waits for the gate of its name, its resume completes and its
// abort handler ends it aborted. The loop drains between steps, so "until" becomes a check after loop.Run.
type nodeWorld struct {
	t         *testing.T
	mem       *sessiontest.MemStore
	sess      *session.Session
	loop      *session.Loop
	sched     *session.Scheduler
	def       *session.TaskDef
	root      session.ID
	behaviors map[string]nodeBehavior
	gates     map[string]*nodeGate
	log       []string
}

func openNodeWorld(t *testing.T) *nodeWorld {
	t.Helper()
	w := &nodeWorld{t: t, mem: sessiontest.New(), loop: &session.Loop{}, behaviors: map[string]nodeBehavior{}, gates: map[string]*nodeGate{}}
	w.sess = newSession(w.mem, w.mem.NextID())
	w.def = &session.TaskDef{Name: "test.node", Version: 1, Initial: func(any) any { return obj("phase", "run") }}
	if _, err := w.sess.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		w.root = idOf(root)
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	impl := &session.TaskImpl{Def: *w.def}
	impl.Phase = func(rt *session.Runtime, rec *jv.Object, done func(error)) {
		name := gstr(rec, "input")
		phase := gstr(get(get(rec, "state").(*jv.Object), "checkpoint").(*jv.Object), "phase")
		w.log = append(w.log, phase+":"+name)
		behavior := w.behaviors[name]
		switch {
		case phase == "run" && behavior.run != nil:
			behavior.run(rt, done)
		case phase == "run":
			w.await(name, rt.Signal(), func(aborted bool) {
				if aborted {
					done(rt.Signal().Err())
					return
				}
				w.commit(rt, func(*session.Tx) (*jv.Object, error) { return nodeEnd(name), nil }, done)
			})
		case behavior.resume != nil:
			behavior.resume(rt, done)
		default:
			w.commit(rt, func(*session.Tx) (*jv.Object, error) { return nodeEnd(name), nil }, done)
		}
	}
	impl.Abort = func(rt *session.Runtime, rec *jv.Object, done func(error)) {
		name := gstr(rec, "input")
		w.log = append(w.log, "abort:"+name)
		if abort := w.behaviors[name].abort; abort != nil {
			abort(rt, done)
			return
		}
		w.commit(rt, func(*session.Tx) (*jv.Object, error) {
			return obj("status", session.StatusTerminal, "outcome", obj("status", "aborted", "result", name)), nil
		}, done)
	}
	registry := snap{w.def.Name: impl}
	w.sched = session.NewScheduler(w.sess, w.loop, session.Env{
		Snapshot:       func() session.Snapshot { return registry },
		Report:         func(err error, _ *jv.Object) { t.Logf("report: %v", err) },
		Now:            func() float64 { return 1000 },
		SettleOutcome:  func(*session.Tx, *jv.Object, *jv.Object) error { return nil },
		WithdrawInputs: func(*session.Tx, session.ID) error { return nil },
		Timer:          func(float64, func()) func() { return func() {} },
	})
	w.sched.Open(func(err error) {
		if err != nil {
			t.Error(err)
		}
	})
	w.sched.Resume()
	w.run()
	return w
}

func (w *nodeWorld) run() {
	w.t.Helper()
	if err := w.loop.Run(); err != nil {
		w.t.Fatal(err)
	}
}

func (w *nodeWorld) script(name string, behavior nodeBehavior) { w.behaviors[name] = behavior }

func (w *nodeWorld) gate(name string) *nodeGate {
	g := w.gates[name]
	if g == nil {
		g = &nodeGate{}
		w.gates[name] = g
	}
	return g
}

// await calls then once the gate opens, or with aborted once signal aborts first.
func (w *nodeWorld) await(name string, signal *session.Cancel, then func(aborted bool)) {
	g := w.gate(name)
	if g.open {
		w.loop.Defer(func() { then(false) })
		return
	}
	fired := false
	remove := signal.OnAbort(func() {
		if !fired {
			fired = true
			w.loop.Defer(func() { then(true) })
		}
	})
	g.waiters = append(g.waiters, func() {
		if !fired {
			fired = true
			remove()
			w.loop.Defer(func() { then(false) })
		}
	})
}

// open opens a gate and drains the loop.
func (w *nodeWorld) open(name string) {
	g := w.gate(name)
	g.open = true
	waiters := g.waiters
	g.waiters = nil
	for _, waiter := range waiters {
		waiter()
	}
	w.run()
}

func (w *nodeWorld) commit(rt *session.Runtime, change func(tx *session.Tx) (*jv.Object, error), done func(error)) {
	rt.Commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) { return change(tx) }, done)
}

// commitThen is a commit whose phase goes on with next once it lands.
func (w *nodeWorld) commitThen(rt *session.Runtime, change func(tx *session.Tx) (*jv.Object, error), done func(error), next func()) {
	w.commit(rt, change, func(err error) {
		if err != nil {
			done(err)
			return
		}
		next()
	})
}

// create makes one conversation-owned node per name in one commit and drains the loop.
func (w *nodeWorld) create(ids map[string]session.ID, names ...string) {
	w.t.Helper()
	if _, err := w.sess.Commit(func(tx *session.Tx) error {
		for _, name := range names {
			id, err := tx.CreateTask(w.def, name, session.TaskOptions{ConversationID: w.root})
			if err != nil {
				return err
			}
			ids[name] = id
		}
		return nil
	}, session.TxScope{}); err != nil {
		w.t.Fatal(err)
	}
	w.run()
}

func (w *nodeWorld) record(id session.ID) *jv.Object {
	w.t.Helper()
	rec, err := w.mem.Task(id)
	if err != nil || rec == nil {
		w.t.Fatalf("task %d: %v %v", id, rec, err)
	}
	return rec
}

func (w *nodeWorld) status(id session.ID) string { return taskStatusOf(w.record(id)) }

// outcome is the status of the task's outcome, or its state's status while it has none.
func (w *nodeWorld) outcome(id session.ID) string {
	state := get(w.record(id), "state").(*jv.Object)
	if outcome, ok := get(state, "outcome").(*jv.Object); ok {
		return gstr(outcome, "status")
	}
	return gstr(state, "status")
}

func (w *nodeWorld) message(id session.ID) string {
	state := get(w.record(id), "state").(*jv.Object)
	outcome, _ := get(state, "outcome").(*jv.Object)
	if outcome == nil {
		return ""
	}
	failure, _ := get(outcome, "error").(*jv.Object)
	if failure == nil {
		return ""
	}
	return gstr(failure, "message")
}

func nodeEnd(name string) *jv.Object {
	return obj("status", session.StatusTerminal, "outcome", obj("status", "completed", "result", name))
}

// nodeWaitOn is waitOn of harness-structured.test.ts (:153): wait on ids, resuming in round 1.
func nodeWaitOn(policy string, ids ...session.ID) *jv.Object {
	on := make([]any, len(ids))
	for i, id := range ids {
		on[i] = float64(id)
	}
	return obj("status", session.StatusWaiting, "checkpoint", obj("phase", "resume", "round", float64(1)), "on", on, "policy", policy)
}

func nodeSpawn(tx *session.Tx, def *session.TaskDef, owner session.ID, name string) (session.ID, error) {
	return tx.CreateTask(def, name, session.TaskOptions{OwnerTask: owner})
}

// spawnAndFinish is harness-structured.test.ts spawnAndFinish (:1445): name spawns child and finishes, so the live child
// holds it as completing.
func (w *nodeWorld) spawnAndFinish(name, child string, ids map[string]session.ID) {
	w.script(name, nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
		w.commit(rt, func(tx *session.Tx) (*jv.Object, error) {
			id, err := nodeSpawn(tx, w.def, rt.TaskID(), child)
			ids[child] = id
			return obj("status", session.StatusRunning, "checkpoint", obj("phase", "resume", "round", float64(1))), err
		}, done)
	}})
}

// waitsOn scripts name's run to wait on the task ids names when it runs.
func (w *nodeWorld) waitsOn(name string, ids map[string]session.ID, on ...string) {
	w.script(name, nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
		w.commit(rt, func(*session.Tx) (*jv.Object, error) {
			targets := make([]session.ID, len(on))
			for i, each := range on {
				targets[i] = ids[each]
			}
			return nodeWaitOn("allSettled", targets...), nil
		}, done)
	}})
}

func sortedOutcomes(w *nodeWorld, ids ...session.ID) []string {
	var out []string
	for _, id := range ids {
		out = append(out, w.outcome(id))
	}
	slices.Sort(out)
	return out
}

// Pi 1.2.0 (eb2fce43) harness-structured.test.ts describe("waiting") :461-694 and scheduler.ts #validateWait and
// #waitCycle (:1472-1554): a wait may not close a cycle in the wait-for graph of waits (of tasks that are not abort-marked)
// and holds of live ordinary owned work. The rejected wait faults its task with "Task X cannot wait on Y: X -> … -> X is a
// wait cycle".
func TestWaitCycles(t *testing.T) {
	t.Run("rejects waits on itself, its owner, a missing task, and failFast on a task it does not own", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		w.create(ids, "other")
		for _, c := range []struct {
			name    string
			on      func(rt *session.Runtime) session.ID
			policy  string
			message string
		}{
			{"self", func(rt *session.Runtime) session.ID { return rt.TaskID() }, "allSettled", "is a wait cycle"},
			{"missing", func(*session.Runtime) session.ID { return 999_999 }, "allSettled", "does not exist"},
			{"foreign", func(*session.Runtime) session.ID { return ids["other"] }, "failFast", "only on tasks it owns"},
		} {
			w.script(c.name, nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
				w.commit(rt, func(*session.Tx) (*jv.Object, error) { return nodeWaitOn(c.policy, c.on(rt)), nil }, done)
			}})
			w.create(ids, c.name)
			if got, message := w.outcome(ids[c.name]), w.message(ids[c.name]); got != "faulted" || !strings.Contains(message, c.message) {
				t.Fatalf("%s: outcome %s %q, want faulted containing %q", c.name, got, message, c.message)
			}
		}
		if want := "Task " + strconv.FormatInt(ids["self"], 10) + " cannot wait on " + strconv.FormatInt(ids["self"], 10) + ": " +
			strconv.FormatInt(ids["self"], 10) + " -> " + strconv.FormatInt(ids["self"], 10) + " is a wait cycle"; w.message(ids["self"]) != want {
			t.Fatalf("self: %q, want %q", w.message(ids["self"]), want)
		}
		// A child waiting on its owner could never resume.
		w.waitsOn("child", ids, "parent")
		w.script("parent", nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
			w.commit(rt, func(tx *session.Tx) (*jv.Object, error) {
				child, err := nodeSpawn(tx, w.def, rt.TaskID(), "child")
				ids["child"] = child
				return nodeWaitOn("allSettled", child), err
			}, done)
		}})
		w.create(ids, "parent")
		if got := w.outcome(ids["parent"]); got != "completed" {
			t.Fatalf("parent: %s", got)
		}
		if got, message := w.outcome(ids["child"]), w.message(ids["child"]); got != "faulted" || !strings.Contains(message, "is a wait cycle") {
			t.Fatalf("child: outcome %s %q, want faulted: is a wait cycle", got, message)
		}
		w.open("other")
	})

	// #10411; this test and the next two are from #10533 by @odysseus0.
	t.Run("rejects the wait that closes a cycle", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		w.waitsOn("a", ids, "b")
		w.waitsOn("b", ids, "a")
		w.create(ids, "a", "b")
		// a -> b and b -> a are waits, and whichever comes second closes the cycle.
		if got := sortedOutcomes(w, ids["a"], ids["b"]); !slices.Equal(got, []string{"completed", "faulted"}) {
			t.Fatalf("outcomes %v, want [completed faulted]", got)
		}
	})

	t.Run("rejects the wait that closes a cycle through a held owner", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		w.spawnAndFinish("held", "child", ids)
		w.waitsOn("child", ids, "other")
		w.waitsOn("other", ids, "held")
		w.create(ids, "held", "other")
		// held -> child is a hold; child -> other -> held are waits, and whichever wait comes second closes the cycle.
		if got := w.outcome(ids["held"]); got != "completed" {
			t.Fatalf("held: %s", got)
		}
		if got := sortedOutcomes(w, ids["child"], ids["other"]); !slices.Equal(got, []string{"completed", "faulted"}) {
			t.Fatalf("outcomes %v, want [completed faulted]", got)
		}
	})

	t.Run("rejects a cycle through holds two levels deep", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		w.spawnAndFinish("held", "child", ids)
		w.spawnAndFinish("child", "grandchild", ids)
		w.waitsOn("grandchild", ids, "other")
		w.waitsOn("other", ids, "held")
		w.create(ids, "held", "other")
		// held -> child -> grandchild are holds; grandchild -> other -> held are waits.
		if ids["grandchild"] == 0 {
			t.Fatal("the grandchild was never spawned")
		}
		if got := sortedOutcomes(w, ids["grandchild"], ids["other"]); !slices.Equal(got, []string{"completed", "faulted"}) {
			t.Fatalf("outcomes %v, want [completed faulted]", got)
		}
		if got := w.outcome(ids["child"]); got != "completed" {
			t.Fatalf("child: %s", got)
		}
	})

	t.Run("lets a background task wait on the owner of its conversation", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		w.waitsOn("background", ids, "owner")
		if _, err := w.sess.Commit(func(tx *session.Tx) error {
			owner, err := tx.CreateTask(w.def, "owner", session.TaskOptions{ConversationID: w.root})
			if err != nil {
				return err
			}
			ids["owner"] = owner
			conversation, err := tx.CreateConversation(session.Ownership{Task: owner})
			if err != nil {
				return err
			}
			ids["background"], err = tx.CreateTask(w.def, "background", session.TaskOptions{ConversationID: idOf(conversation), Background: true})
			return err
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		w.run()
		// background -> owner is a wait but owner -> background is no hold, so there is no cycle.
		if got := w.status(ids["background"]); got != session.StatusWaiting {
			t.Fatalf("background is %s (%q), want waiting", got, w.message(ids["background"]))
		}
		w.open("owner")
		if got := w.outcome(ids["background"]); got != "completed" {
			t.Fatalf("background: %s", got)
		}
	})

	// #10411: the issue's orchestrator, worker, and reviewer.
	t.Run("rejects a cycle of a hold and two waits that each look reasonable", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		// The orchestrator spawns a worker and finishes; the running worker holds it as completing.
		w.spawnAndFinish("orchestrator", "worker", ids)
		// The reviewer waits for the orchestrator's result.
		w.script("reviewer", nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
			w.commitThen(rt, func(*session.Tx) (*jv.Object, error) { return nodeWaitOn("allSettled", ids["orchestrator"]), nil }, done, func() {
				w.open("reviewer waits")
				done(nil)
			})
		}})
		// The worker waits for the reviewer, closing the cycle.
		w.script("worker", nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
			w.await("reviewer waits", rt.Signal(), func(aborted bool) {
				if aborted {
					done(rt.Signal().Err())
					return
				}
				w.commit(rt, func(*session.Tx) (*jv.Object, error) { return nodeWaitOn("allSettled", ids["reviewer"]), nil }, done)
			})
		}})
		w.create(ids, "orchestrator")
		if got := w.status(ids["orchestrator"]); got != session.StatusCompleting {
			t.Fatalf("orchestrator is %s, want completing", got)
		}
		w.create(ids, "reviewer")
		var outcomes []string
		for _, name := range []string{"orchestrator", "worker", "reviewer"} {
			outcomes = append(outcomes, w.outcome(ids[name]))
		}
		if !slices.Equal(outcomes, []string{"completed", "faulted", "completed"}) {
			t.Fatalf("outcomes %v, want [completed faulted completed]", outcomes)
		}
		format := func(names ...string) string {
			parts := make([]string, len(names))
			for i, name := range names {
				parts[i] = strconv.FormatInt(ids[name], 10)
			}
			return strings.Join(parts, " -> ")
		}
		want := format("worker", "reviewer", "orchestrator", "worker") + " is a wait cycle"
		if got := w.message(ids["worker"]); !strings.Contains(got, want) {
			t.Fatalf("worker message %q, want it to contain %q", got, want)
		}
	})

	t.Run("allows a diamond of waits", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		// A diamond: top waits on left and right, both wait on bottom. No cycle.
		w.waitsOn("top", ids, "left", "right")
		w.waitsOn("left", ids, "bottom")
		w.waitsOn("right", ids, "bottom")
		w.create(ids, "bottom", "left", "right", "top")
		w.open("bottom")
		if got := w.outcome(ids["top"]); got != "completed" {
			t.Fatalf("top: %s (%q)", got, w.message(ids["top"]))
		}
	})

	t.Run("follows no wait of an abort-marked task, which waits only for its owned work", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		// b owns c and waits on a; aborting b leaves c's cleanup pending, so b waits only for c.
		w.script("b", nodeBehavior{run: func(rt *session.Runtime, done func(error)) {
			w.commit(rt, func(tx *session.Tx) (*jv.Object, error) {
				c, err := nodeSpawn(tx, w.def, rt.TaskID(), "c")
				ids["c"] = c
				return nodeWaitOn("allSettled", ids["a"]), err
			}, done)
		}})
		w.script("c", nodeBehavior{abort: func(rt *session.Runtime, done func(error)) {
			w.await("cleanup", nil, func(bool) {
				w.commit(rt, func(*session.Tx) (*jv.Object, error) {
					return obj("status", session.StatusTerminal, "outcome", obj("status", "aborted", "result", "c")), nil
				}, done)
			})
		}})
		w.script("a", nodeBehavior{
			run: func(rt *session.Runtime, done func(error)) {
				w.await("a waits", rt.Signal(), func(aborted bool) {
					if aborted {
						done(rt.Signal().Err())
						return
					}
					w.commit(rt, func(*session.Tx) (*jv.Object, error) { return nodeWaitOn("allSettled", ids["b"]), nil }, done)
				})
			},
			resume: func(rt *session.Runtime, done func(error)) {
				w.commit(rt, func(*session.Tx) (*jv.Object, error) { return nodeEnd("a"), nil }, done)
			},
		})
		w.create(ids, "a", "b")
		if got := w.status(ids["b"]); got != session.StatusWaiting {
			t.Fatalf("b is %s, want waiting", got)
		}
		var result string
		w.sched.Abort(ids["b"], false, nil, func(got string, err error) {
			if err != nil {
				t.Error(err)
			}
			result = got
		})
		w.run()
		if result != "marked" {
			t.Fatalf("abort of b = %q, want marked", result)
		}
		if !slices.Contains(w.log, "abort:c") {
			t.Fatalf("log %v, want abort:c", w.log)
		}
		// a -> b would close a -> b -> a through b's stale wait; b now waits only for c.
		w.open("a waits")
		if got := w.status(ids["a"]); got != session.StatusWaiting {
			t.Fatalf("a is %s (%q), want waiting", got, w.message(ids["a"]))
		}
		w.open("cleanup")
		if got := w.outcome(ids["b"]); got != "aborted" {
			t.Fatalf("b: %s", got)
		}
		if got := w.outcome(ids["a"]); got != "completed" {
			t.Fatalf("a: %s", got)
		}
	})

	t.Run("rejects a longer cycle of waits", func(t *testing.T) {
		w := openNodeWorld(t)
		ids := map[string]session.ID{}
		names := []string{"x", "y", "z"}
		for index, name := range names {
			w.waitsOn(name, ids, names[(index+1)%len(names)])
		}
		w.create(ids, names...)
		counts := map[string]int{}
		for _, name := range names {
			counts[w.outcome(ids[name])]++
		}
		if counts["faulted"] != 1 || counts["completed"] != 2 {
			t.Fatalf("outcomes %v, want one faulted and two completed", counts)
		}
	})
}
