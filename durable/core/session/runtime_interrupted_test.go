package session_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// Pi b3a29f11 scheduler.ts runtime.commit: a task whose commit ends it failed with an error whose detail.reason is restart_limit
// or interrupted is reported as task_interrupted with that reason and the task's restarts, whoever the task is: the report is the
// commit wrapper's, after the commit and before the caller resumes, not a built-in task's.
func TestACommitThatEndsAnyTaskForCrashesReportsTaskInterrupted(t *testing.T) {
	failed := func(detail any) *jv.Object {
		err := obj("message", "ended for crashes")
		if detail != nil {
			err.Set("detail", detail)
		}
		return obj("status", session.StatusTerminal, "outcome", obj("status", "failed", "error", err))
	}
	completed := obj("status", session.StatusTerminal, "outcome", obj("status", "completed", "result", jv.NewObject()))
	aborted := obj("status", session.StatusTerminal, "outcome", obj("status", "aborted"))
	cases := []struct {
		name  string
		crash bool
		next  *jv.Object
		want  string
	}{
		{"interrupted", true, failed(obj("reason", "interrupted")), `"reason":"interrupted","restarts":1}`},
		{"restart_limit", true, failed(obj("reason", "restart_limit")), `"reason":"restart_limit","restarts":1}`},
		{"no restart count", false, failed(obj("reason", "restart_limit")), `"reason":"restart_limit","restarts":0}`},
		{"another reason", true, failed(obj("reason", "model_error")), ""},
		{"no detail", true, failed(nil), ""},
		{"a detail that is not an object", true, failed("restart_limit"), ""},
		{"completed", true, completed, ""},
		{"aborted", true, aborted, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mem := sessiontest.New()
			first := newSession(mem, mem.NextID())
			def := &session.TaskDef{Name: "t.ext", Version: 1, Initial: func(any) any { return jv.NewObject() }}
			var id session.ID
			if _, err := first.Commit(func(tx *session.Tx) error {
				root, err := tx.CreateRootConversation()
				if err != nil {
					return err
				}
				id, err = tx.CreateTask(def, nil, session.TaskOptions{ConversationID: idOf(root)})
				return err
			}, session.TxScope{}); err != nil {
				t.Fatal(err)
			}
			run := func(s *session.Session, phase func(rt *session.Runtime, task *jv.Object, done func(error)), report func(error, *jv.Object)) {
				registry := snap{"t.ext": {Def: *def, Extension: "ext", Phase: phase}}
				loop := &session.Loop{}
				sched := session.NewScheduler(s, loop, session.Env{Snapshot: func() session.Snapshot { return registry }, Report: report, Now: func() float64 { return 1 }, SettleOutcome: func(*session.Tx, *jv.Object, *jv.Object) error { return nil }})
				sched.Open(func(err error) {
					if err != nil {
						t.Error(err)
					}
				})
				sched.Resume()
				if err := loop.Run(); err != nil {
					t.Fatal(err)
				}
			}
			if c.crash {
				// A host that dies while the task runs: the next open counts one restart.
				run(first, func(*session.Runtime, *jv.Object, func(error)) {}, func(error, *jv.Object) {})
			}
			var reports []string
			committed := -1
			second := newSession(mem, mem.NextID())
			run(second, func(rt *session.Runtime, _ *jv.Object, done func(error)) {
				rt.Commit(func(*session.Tx, *jv.Object) (*jv.Object, error) { return c.next, nil }, func(err error) {
					if err != nil {
						t.Error(err)
					}
					committed = len(reports)
					done(nil)
				})
			}, func(err error, info *jv.Object) {
				if err == nil || err.Error() != "ended for crashes" {
					t.Errorf("reported %v", err)
				}
				reports = append(reports, string(jv.Append(nil, info)))
			})
			if committed < 0 {
				t.Fatal("the commit never returned")
			}
			if c.want == "" {
				if len(reports) != 0 {
					t.Fatalf("reports %q, want none", reports)
				}
				return
			}
			if len(reports) != 1 || committed != 1 {
				t.Fatalf("reports %q, %d of them before the commit returned, want one before", reports, committed)
			}
			want := `{"type":"task_interrupted","task":{"id":` + strconv.Itoa(int(id)) + `,"kind":"t.ext","conversationId":`
			if !strings.HasPrefix(reports[0], want) || !strings.HasSuffix(reports[0], `"extension":"ext",`+c.want) {
				t.Fatalf("report %s, want %s…\"extension\":\"ext\",%s", reports[0], want, c.want)
			}
		})
	}
}
