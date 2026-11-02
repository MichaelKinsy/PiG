package session_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// The scheduler scenarios run testdata/sched/*.json through the pinned Pi's
// TaskScheduler (testdata/sched_oracle.mjs) and through this package's
// Scheduler, and compare the whole event log: every storage commit in order,
// phase notes, memo winners, settlements, reports and waiter results.

func schedOracle(t *testing.T, path string) []any {
	t.Helper()
	out, err := exec.Command("node", "--conditions=source", "testdata/sched_oracle.mjs", path).Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("oracle: %v\n%s", err, ee.Stderr)
		}
		t.Skipf("node unavailable: %v", err)
	}
	v, err := jv.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	return v.([]any)
}

func TestSchedulerMatchesPiDurable(t *testing.T) {
	files, _ := filepath.Glob("testdata/sched/*.json")
	if len(files) == 0 {
		t.Fatal("no scheduler scenarios")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			want := schedOracle(t, file)
			got := runSched(t, file)
			n := max(len(got), len(want))
			for i := range n {
				var g, w string
				if i < len(got) {
					g = jv.Stringify(got[i])
				}
				if i < len(want) {
					w = jv.Stringify(want[i])
				}
				if g != w {
					t.Fatalf("event %d differs\n got  %s\n want %s\n(got %d events, want %d)", i, g, w, len(got), len(want))
				}
			}
		})
	}
}

// logStore logs each commit one tick after Storage received it, as the oracle's wrapper does (`queueMicrotask` in the receipt of each stored
// batch): the entry is queued in the tick the writer's call stores the batch, and the Harness clock advances with it.
//
// A scenario's "storage" shapes the Storage as the oracle does (testdata/sched_oracle.mjs): the batch at refuseAt (counted from the start) is
// refused, the one at rejectAt fails its call.
type logStore struct {
	*sessiontest.MemStore
	log    *[]any
	logged *int
	loop   *session.Loop
	// stored counts the batches stored so far; refuse and reject name the batch that is refused or fails the call (-1: none).
	stored         int
	refuse, reject int
}

func (l *logStore) Commit(writes []session.Write, next session.ID) (session.Seq, error) {
	if l.stored == l.refuse {
		return 0, &session.RefusedError{Err: errors.New("refused")}
	}
	if l.stored == l.reject {
		return 0, errors.New("rejected")
	}
	l.stored++
	seq, err := l.MemStore.Commit(writes, next)
	if err == nil {
		ws := make([]any, len(writes))
		for i, w := range writes {
			ws[i] = writeValue(w)
		}
		entry := obj("t", "commit", "seq", float64(seq), "writes", ws)
		l.loop.Defer(func() {
			*l.logged++
			*l.log = append(*l.log, entry)
		})
	}
	return seq, err
}

type registry struct{ installed map[string]*session.TaskImpl }

func (r *registry) Task(name string) *session.TaskImpl { return r.installed[name] }

func resolveRef(v any, created []session.ID) any {
	if s, ok := v.(string); ok && len(s) > 2 && s[:2] == "$c" {
		n := 0
		for _, c := range s[2:] {
			n = n*10 + int(c-'0')
		}
		return float64(created[n])
	}
	return v
}

func runSched(t *testing.T, file string) []any {
	sc := parseFile(t, file)
	var events []any
	logf := func(kv ...any) { events = append(events, obj(kv...)) }
	mem := sessiontest.New()
	loop := &session.Loop{}
	logged := 0
	shape, _ := get(sc, "storage").(*jv.Object)
	storageTicks, refuseAt, rejectAt := 0, -1, -1
	if shape != nil {
		storageTicks = int(gint(shape, "ticks"))
		if shape.Has("refuseAt") {
			refuseAt = int(gint(shape, "refuseAt"))
		}
		if shape.Has("rejectAt") {
			rejectAt = int(gint(shape, "rejectAt"))
		}
	}
	store := &logStore{MemStore: mem, log: &events, logged: &logged, loop: loop, refuse: refuseAt, reject: rejectAt}
	loop.Elapsed = sessiontest.Elapsed(hopcost.Memory)
	// The oracle's Storage is an async function around MemoryStorage that waits storageTicks passes first (testdata/sched_oracle.mjs): MemoryStorage
	// stores in the tick it is called, one tick after the wait when there is one, and the wrapper's own promise settles when it returns
	// MemoryStorage's (two passes later), or at once after awaiting a call whose batch was cut off by a refusal or a rejection.
	loop.StoreTicks = func(batches [][]session.Write) (int, int, int) {
		receive := 0
		if storageTicks > 0 {
			receive = storageTicks + 1
		}
		cut := len(batches)
		for i := range batches {
			if store.stored+i == refuseAt || store.stored+i == rejectAt {
				cut = i
				break
			}
		}
		if cut < len(batches) {
			return 0, receive, hopcost.StoreCallResume(receive + 1)
		}
		return 0, receive, hopcost.StoreCallResume(receive + 2)
	}
	now := func() float64 { return 1000 + float64(logged) }
	if gstr(sc, "clock") == "fixed" {
		now = func() float64 { return 1000 }
	}
	sess := session.New(store, mem.NextID(), now)
	sess.SetHopProfile(hopcost.Memory)
	loop.Bind(sess)

	gates := map[string][]func(){}
	released := map[string]bool{}
	var impls map[string]*session.TaskImpl
	var reg *registry

	runSteps := func(steps []any, task *jv.Object, rt *session.Runtime, done func(error)) {
		i := 0
		var next func()
		next = func() {
			if i >= len(steps) {
				done(nil)
				return
			}
			step := steps[i].(*jv.Object)
			i++
			tid := gint(task, "id")
			switch {
			case get(step, "commit") != nil:
				c := get(step, "commit").(*jv.Object)
				rt.Commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
					var created []session.ID
					ops, _ := get(c, "ops").([]any)
					for _, o := range ops {
						op := o.(*jv.Object)
						switch gstr(op, "op") {
						case "childTask":
							id, err := tx.CreateTask(&impls[gstr(op, "task")].Def, get(op, "input"), session.TaskOptions{OwnerTask: tid})
							if err != nil {
								return nil, err
							}
							created = append(created, id)
						case "task":
							id, err := tx.CreateTask(&impls[gstr(op, "task")].Def, get(op, "input"), session.TaskOptions{Background: get(op, "background") == true, ConversationID: gint(op, "conversationId")})
							if err != nil {
								return nil, err
							}
							created = append(created, id)
						case "appendEntry":
							if _, err := tx.AppendEntry(gint(task, "conversationId"), jv.Clone(get(op, "draft")).(*jv.Object)); err != nil {
								return nil, err
							}
						case "ownedConversation":
							if _, err := tx.CreateConversation(session.Ownership{Task: tid}); err != nil {
								return nil, err
							}
						}
					}
					n, ok := get(c, "next").(*jv.Object)
					if !ok {
						return nil, nil
					}
					state := jv.Clone(n).(*jv.Object)
					if on, ok := state.Get("on"); ok {
						list := on.([]any)
						for k := range list {
							list[k] = resolveRef(list[k], created)
						}
					}
					return state, nil
				}, func(err error) {
					if err != nil {
						done(err)
						return
					}
					next()
				})
			case get(step, "memo") != nil:
				m := get(step, "memo").([]any)
				rt.Memo(m[0].(string), jv.Clone(m[1]), func(w any, err error) {
					if err != nil {
						done(err)
						return
					}
					logf("t", "memo", "task", float64(tid), "name", m[0], "winner", w)
					next()
				})
			case get(step, "await") != nil:
				g := gstr(step, "await")
				if released[g] {
					loop.Defer(next)
				} else {
					gates[g] = append(gates[g], func() { loop.Defer(next) })
				}
			case get(step, "waitForTask") != nil:
				on := get(step, "waitForTask")
				rt.WaitForTask(session.ID(on.(float64)), func(rec *jv.Object, err error, _ bool) {
					// The task's call goes through the scheduler's `#read`, an async function that returns the read's promise: the task resumes two
					// ticks after the promise settles.
					loop.Wait(2, markResumer(func() {
						if err != nil {
							logf("t", "waited", "task", float64(tid), "on", on, "error", err.Error())
						} else {
							st, _ := getStrField(rec)
							logf("t", "waited", "task", float64(tid), "on", on, "status", st)
						}
						next()
					}))
				})
			case get(step, "throw") != nil:
				done(errors.New(gstr(step, "throw")))
			case get(step, "log") != nil:
				logf("t", "phase", "task", float64(tid), "note", gstr(step, "log"))
				next()
			}
		}
		next()
	}

	impls = map[string]*session.TaskImpl{}
	byName := map[string]*session.TaskImpl{}
	if tasks, ok := get(sc, "tasks").(*jv.Object); ok {
		tasks.Entries(func(key string, v any) bool {
			spec := v.(*jv.Object)
			phases := get(spec, "phases").(*jv.Object)
			initialPhase := "start"
			if p := gstr(spec, "initialPhase"); p != "" {
				initialPhase = p
			}
			impl := &session.TaskImpl{Def: session.TaskDef{Name: gstr(spec, "name"), Version: int(gint(spec, "version")), Initial: func(input any) any {
				return obj("phase", initialPhase, "input", jv.Clone(input))
			}}}
			impl.Phase = func(rt *session.Runtime, task *jv.Object, done func(error)) {
				cp := get(get(task, "state").(*jv.Object), "checkpoint").(*jv.Object)
				steps, ok := get(phases, gstr(cp, "phase")).([]any)
				if !ok {
					done(errors.New("phase handler missing"))
					return
				}
				runSteps(steps, task, rt, done)
			}
			impl.Abort = func(rt *session.Runtime, task *jv.Object, done func(error)) {
				steps, _ := get(spec, "abort").([]any)
				runSteps(steps, task, rt, done)
			}
			if get(spec, "migrate") == true {
				impl.Migrate = func(input, cp any, from int) (any, any, error) {
					c := cp.(*jv.Object).Shallow()
					c.Set("migratedFrom", float64(from))
					return input, c, nil
				}
			}
			impls[key], byName[gstr(spec, "name")] = impl, impl
			return true
		})
	}
	reg = &registry{installed: map[string]*session.TaskImpl{}}
	if init, ok := get(sc, "initialRegistry").([]any); ok {
		for _, k := range init {
			reg.installed[impls[k.(string)].Def.Name] = impls[k.(string)]
		}
	} else {
		for k, impl := range impls {
			_ = k
			reg.installed[impl.Def.Name] = impl
		}
	}

	sched := session.NewScheduler(sess, loop, session.Env{
		Snapshot: func() session.Snapshot { return reg },
		Report:   func(err error, _ *jv.Object) { logf("t", "report", "message", err.Error()) },
		Now:      now,
		// The callbacks of a job run at the tick of the job's duration their Pi awaits reach: the log entry follows the ticks the transaction spent.
		SettleOutcome: func(tx *session.Tx, rec *jv.Object, outcome *jv.Object) error {
			task, status := get(rec, "id"), get(outcome, "status")
			loop.Wait(tx.Ticks()-loop.Consumed(), markResumer(func() { logf("t", "settleOutcome", "task", task, "status", status) }))
			return nil
		},
		WithdrawInputs: func(tx *session.Tx, id session.ID) error {
			loop.Wait(tx.Ticks()-loop.Consumed(), markResumer(func() { logf("t", "withdraw", "conversation", float64(id)) }))
			return nil
		},
		Timer: func(at float64, fire func()) func() { return func() {} },
	})
	_ = byName
	// The oracle's Session is a plain one, which drops the failure's report: the Scheduler's report of it is the Harness's.
	sess.OnFail = nil
	if get(sc, "membership") != true {
		// Nor does the oracle's plain Session add the scheduler's membership keys (`membership: true` does).
		sess.MembershipKeys = nil
	}

	watch := func(name string, f func(done func(rec *jv.Object, err error))) {
		f(func(rec *jv.Object, err error) {
			switch {
			case err != nil:
				logf("t", name, "error", err.Error())
			case rec != nil:
				st, _ := getStrField(rec)
				logf("t", name, "ok", st)
			default:
				logf("t", name, "ok", true)
			}
		})
	}

	queueCommit := func(ops []any, done func(error)) {
		loop.Line(func() error {
			_, err := sess.Commit(func(tx *session.Tx) error {
				for _, o := range ops {
					op := o.(*jv.Object)
					var err error
					switch gstr(op, "op") {
					case "rootConversation":
						_, err = tx.CreateRootConversation()
					case "createConversation":
						_, err = tx.CreateConversation(session.Ownership{Task: gint(op, "ownerTask")})
					case "createTask":
						opts := session.TaskOptions{}
						if oo, ok := get(op, "opts").(*jv.Object); ok {
							opts = session.TaskOptions{OwnerTask: gint(oo, "ownerTask"), ConversationID: gint(oo, "conversationId"), Background: get(oo, "background") == true}
						}
						input := get(op, "input")
						_, err = tx.CreateTask(&impls[gstr(op, "task")].Def, input, opts)
					case "appendEntry":
						_, err = tx.AppendEntry(gint(op, "conversationId"), jv.Clone(get(op, "draft")).(*jv.Object))
					case "createSubmission":
						_, err = tx.CreateSubmission(jv.Clone(get(op, "create")).(*jv.Object))
					}
					if err != nil {
						return err
					}
				}
				return nil
			}, session.TxScope{})
			return err
		}, done)
	}

	for i, cv := range get(sc, "cmds").([]any) {
		cmd := cv.(*jv.Object)
		logf("t", "cmd", "i", float64(i), "cmd", gstr(cmd, "cmd"))
		switch gstr(cmd, "cmd") {
		case "commit":
			queueCommit(get(cmd, "ops").([]any), func(err error) {
				if err != nil {
					logf("t", "cmdError", "i", float64(i), "message", err.Error())
				}
			})
		// commits queues several on the line in one tick: they overlap in the Storage.
		case "commits":
			for j, ops := range get(cmd, "each").([]any) {
				queueCommit(ops.([]any), func(err error) {
					if err != nil {
						logf("t", "cmdError", "i", float64(i), "j", float64(j), "message", err.Error())
						return
					}
					logf("t", "committed", "i", float64(i), "j", float64(j))
				})
			}
		case "open":
			sched.Open(func(err error) {
				if err != nil {
					logf("t", "cmdError", "i", float64(i), "message", err.Error())
				}
			})
		case "resume":
			sched.Resume()
		case "abort":
			watch("abort", func(done func(*jv.Object, error)) {
				sched.Abort(gint(cmd, "id"), false, nil, func(_ string, err error) { done(nil, err) })
			})
		case "abortConversation":
			watch("abortConversation", func(done func(*jv.Object, error)) {
				sched.AbortConversation(gint(cmd, "id"), get(cmd, "background") == true, nil, func(err error) { done(nil, err) })
			})
		case "release":
			names := []string{gstr(cmd, "gate")}
			if list, ok := get(cmd, "gates").([]any); ok {
				names = nil
				for _, n := range list {
					names = append(names, n.(string))
				}
			}
			for _, g := range names {
				released[g] = true
				for _, f := range gates[g] {
					f()
				}
				delete(gates, g)
			}
		case "waitForTask":
			id := gint(cmd, "id")
			watch("waitForTask:"+jv.FormatNumber(float64(id)), func(done func(*jv.Object, error)) { sched.WaitForTask(id, nil, done) })
		case "waitForIdle":
			id := gint(cmd, "conv")
			watch("waitForIdle:"+jv.FormatNumber(float64(id)), func(done func(*jv.Object, error)) {
				sched.WaitForIdle(id, nil, func(err error) { done(nil, err) })
			})
		case "registry":
			reg.installed = map[string]*session.TaskImpl{}
			for _, k := range get(cmd, "installed").([]any) {
				reg.installed[impls[k.(string)].Def.Name] = impls[k.(string)]
			}
			sched.RegistryChanged()
		case "flush":
		}
		if err := loop.Run(); err != nil {
			t.Fatalf("loop: %v", err)
		}
	}
	return events
}

func getStrField(rec *jv.Object) (string, bool) {
	st, _ := get(rec, "state").(*jv.Object)
	if st == nil {
		return "", false
	}
	o, _ := get(st, "outcome").(*jv.Object)
	if o == nil {
		return "", false
	}
	s, ok := get(o, "status").(string)
	return s, ok
}
