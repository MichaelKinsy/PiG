package session_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// The scenario test runs each testdata/scenarios/*.json through the pinned Pi's pi-durable
// (testdata/oracle.mjs on .upstream/current's session/session.ts over MemoryStorage) and
// through this package over sessiontest.MemStore, and compares every step's
// storage commit and publication byte for byte.

func oracle(t *testing.T, path string) []any {
	t.Helper()
	cmd := exec.Command("node", "--conditions=source", "testdata/oracle.mjs", path)
	out, err := cmd.Output()
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

func TestScenariosMatchPiDurable(t *testing.T) {
	files, _ := filepath.Glob("testdata/scenarios/*.json")
	if len(files) == 0 {
		t.Fatal("no scenarios")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			want := oracle(t, file)
			got := runScenario(t, file)
			if len(got) != len(want) {
				t.Fatalf("steps: got %d want %d", len(got), len(want))
			}
			for i := range want {
				if g, w := jv.Stringify(got[i]), jv.Stringify(want[i]); g != w {
					t.Errorf("step %d\n got  %s\n want %s", i, g, w)
				}
			}
		})
	}
}

func parseFile(t *testing.T, path string) *jv.Object {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jv.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jv.Object)
}

func get(o *jv.Object, k string) any     { v, _ := o.Get(k); return v }
func gstr(o *jv.Object, k string) string { s, _ := get(o, k).(string); return s }
func gint(o *jv.Object, k string) int64 {
	f, _ := get(o, k).(float64)
	return int64(f)
}

func buildDocs(sc *jv.Object) map[string]*session.DocDef {
	docs := map[string]*session.DocDef{}
	if d, ok := get(sc, "docs").(*jv.Object); ok {
		d.Entries(func(name string, v any) bool {
			spec := v.(*jv.Object)
			initial := get(spec, "initial").(*jv.Object)
			def := &session.DocDef{
				Kind: gstr(spec, "kind"), Version: int(gint(spec, "version")), Scope: session.ScopeKind(gstr(spec, "scope")),
				History: session.History(gstr(spec, "history")), Fork: session.ForkPolicy(gstr(spec, "fork")),
				Family: get(spec, "family") == true,
			}
			family := def.Family
			def.Initial = func(seed any) *jv.Object {
				c := jv.Clone(initial).(*jv.Object)
				if family {
					c.Set("seed", jv.Clone(seed))
				}
				return c
			}
			switch gstr(spec, "checkpoint") {
			case "always":
				def.CheckpointWhen = func(*jv.Object, []delta.Op, int) bool { return true }
			case "emptyItems":
				def.CheckpointWhen = func(v *jv.Object, _ []delta.Op, _ int) bool {
					items, _ := get(v, "items").([]any)
					return len(items) == 0
				}
			}
			if m, ok := get(spec, "migrate").(*jv.Object); ok {
				def.Migrate = func(v *jv.Object, _ int) *jv.Object {
					out := v.Shallow()
					m.Entries(func(k string, x any) bool { out.Set(k, jv.Clone(x)); return true })
					return out
				}
			}
			docs[name] = def
			return true
		})
	}
	return docs
}

func buildTasks(sc *jv.Object) map[string]*session.TaskDef {
	tasks := map[string]*session.TaskDef{}
	if d, ok := get(sc, "tasks").(*jv.Object); ok {
		d.Entries(func(name string, v any) bool {
			spec := v.(*jv.Object)
			tasks[name] = &session.TaskDef{Name: gstr(spec, "name"), Version: int(gint(spec, "version")), Initial: func(input any) any {
				o := jv.NewObject()
				o.Set("phase", "start")
				o.Set("input", jv.Clone(input))
				return o
			}}
			return true
		})
	}
	return tasks
}

func docArgs(def *session.DocDef, op *jv.Object) session.DocArgs {
	return session.DocArgs{Owner: gint(op, "owner"), Key: gstr(op, "key"), Seed: get(op, "seed")}
}

func ownership(op *jv.Object, key string) session.Ownership {
	return session.Ownership{Task: gint(op, key)}
}

// applyEdit applies one edit step of the scenario to a draft.
func applyEdit(t *testing.T, root *delta.Obj, s *jv.Object) error {
	t.Helper()
	var cur any = root
	for _, seg := range get(s, "path").([]any) {
		switch c := cur.(type) {
		case *delta.Obj:
			cur = c.Get(seg.(string))
		case *delta.Arr:
			cur = c.Get(int(seg.(float64)))
		}
	}
	op := gstr(s, "op")
	if a, ok := cur.(*delta.Arr); ok {
		switch op {
		case "push":
			_, err := a.Push(get(s, "values").([]any)...)
			return err
		case "unshift":
			_, err := a.Unshift(get(s, "values").([]any)...)
			return err
		case "pop":
			a.Pop()
		case "shift":
			a.Shift()
		case "splice":
			_, err := a.Splice(int(gint(s, "start")), int(gint(s, "remove")), get(s, "items").([]any)...)
			return err
		case "setIndex":
			return a.Set(int(gint(s, "index")), get(s, "value"))
		}
		return nil
	}
	o := cur.(*delta.Obj)
	switch op {
	case "set":
		return o.Set(gstr(s, "key"), get(s, "value"))
	case "delete":
		o.Delete(gstr(s, "key"))
	}
	return nil
}

func runScenario(t *testing.T, file string) []any {
	sc := parseFile(t, file)
	docs, tasks := buildDocs(sc), buildTasks(sc)
	store := sessiontest.New()
	clock := 1000.0
	s := session.New(store, store.NextID(), func() float64 { c := clock; clock++; return c })
	var out []any
	for i, stepV := range get(sc, "steps").([]any) {
		step := stepV.(*jv.Object)
		var scope session.TxScope
		if sv, ok := get(step, "scope").(*jv.Object); ok {
			scope = session.TxScope{ConversationID: gint(sv, "conversationId"), TaskID: gint(sv, "taskId")}
		}
		var reads []any
		pub, err := s.Commit(func(tx *session.Tx) error {
			for _, opV := range get(step, "ops").([]any) {
				op := opV.(*jv.Object)
				var err error
				switch gstr(op, "op") {
				case "rootConversation":
					_, err = tx.CreateRootConversation()
				case "createConversation":
					_, err = tx.CreateConversation(ownership(op, "ownerTask"))
				case "fork":
					_, err = tx.ForkConversation(gint(op, "parent"), gint(op, "at"), ownership(op, "ownerTask"))
				case "appendEntry":
					_, err = tx.AppendEntry(gint(op, "conversationId"), jv.Clone(get(op, "draft")).(*jv.Object))
				case "createTask":
					opts := session.TaskOptions{}
					if o, ok := get(op, "opts").(*jv.Object); ok {
						opts = session.TaskOptions{OwnerTask: gint(o, "ownerTask"), ConversationID: gint(o, "conversationId"), Background: get(o, "background") == true}
					}
					_, err = tx.CreateTask(tasks[gstr(op, "task")], get(op, "input"), opts)
				case "createSubmission":
					_, err = tx.CreateSubmission(get(op, "create").(*jv.Object))
				case "settle":
					err = tx.SettleSubmission(gint(op, "id"), get(op, "settlement").(*jv.Object))
				case "place":
					err = tx.PlaceSubmission(gint(op, "id"), gint(op, "entry"))
				case "updateTask":
					cur, _ := store.Task(gint(op, "id"))
					if cur == nil {
						return errString("Task " + jv.FormatNumber(float64(gint(op, "id"))) + " does not exist")
					}
					next := cur.Shallow()
					get(op, "patch").(*jv.Object).Entries(func(k string, v any) bool { next.Set(k, jv.Clone(v)); return true })
					err = tx.SetTask(next)
				case "doc":
					def := docs[gstr(op, "doc")]
					var draft *delta.Obj
					if draft, err = tx.Doc(def, docArgs(def, op)); err == nil {
						if edits, ok := get(op, "edit").([]any); ok {
							for _, e := range edits {
								if err = applyEdit(t, draft, e.(*jv.Object)); err != nil {
									break
								}
							}
						}
					}
				case "retireDoc":
					def := docs[gstr(op, "doc")]
					err = tx.RetireDoc(def, docArgs(def, op))
				case "readTask":
					v, e := tx.Task(gint(op, "id"))
					reads = append(reads, readResult(v, e))
				case "readConversation":
					v, e := tx.Conversation(gint(op, "id"))
					reads = append(reads, readResult(v, e))
				default:
					t.Fatalf("bad op %s", gstr(op, "op"))
				}
				if err != nil {
					return err
				}
			}
			return nil
		}, scope)
		res := jv.NewObject()
		res.Set("step", float64(i))
		if err != nil {
			res.Set("error", err.Error())
		}
		if len(reads) > 0 {
			res.Set("reads", reads)
		}
		res.Set("commit", commitValue(store, pub))
		res.Set("pub", pubValue(pub))
		out = append(out, res)
	}
	return out
}

type errString string

func (e errString) Error() string { return string(e) }

func readResult(v *jv.Object, err error) any {
	if err != nil {
		return obj("error", errorMessage(err))
	}
	if v == nil {
		return nil
	}
	return v
}

func obj(kv ...any) *jv.Object {
	o := jv.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}
