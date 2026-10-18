package session_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
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

type logStore struct {
	*sessiontest.MemStore
	log *[]any
}

func (l logStore) Commit(writes []session.Write, next session.ID) (session.Seq, error) {
	seq, err := l.MemStore.Commit(writes, next)
	if err == nil {
		ws := make([]any, len(writes))
		for i, w := range writes {
			ws[i] = writeValue(w)
		}
		*l.log = append(*l.log, obj("t", "commit", "seq", float64(seq), "writes", ws))
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
	store := logStore{mem, &events}
	now := func() float64 { return 1000 + float64(len(mem.Commits)) }
	sess := session.New(store, mem.NextID(), now)
	loop := &session.Loop{}

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
					if err != nil {
						logf("t", "waited", "task", float64(tid), "on", on, "error", err.Error())
					} else {
						st, _ := getStrField(rec)
						logf("t", "waited", "task", float64(tid), "on", on, "status", st)
					}
					next()
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
		SettleOutcome: func(tx *session.Tx, rec *jv.Object, outcome *jv.Object) error {
			logf("t", "settleOutcome", "task", get(rec, "id"), "status", get(outcome, "status"))
			return nil
		},
		WithdrawInputs: func(tx *session.Tx, id session.ID) error {
			logf("t", "withdraw", "conversation", float64(id))
			return nil
		},
		Timer: func(at float64, fire func()) func() { return func() {} },
	})
	_ = byName

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

	for i, cv := range get(sc, "cmds").([]any) {
		cmd := cv.(*jv.Object)
		logf("t", "cmd", "i", float64(i), "cmd", gstr(cmd, "cmd"))
		switch gstr(cmd, "cmd") {
		case "commit":
			loop.Line(func() error {
				_, err := sess.Commit(func(tx *session.Tx) error {
					for _, o := range get(cmd, "ops").([]any) {
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
			}, func(err error) {
				if err != nil {
					logf("t", "cmdError", "i", float64(i), "message", err.Error())
				}
			})
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
