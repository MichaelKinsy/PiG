package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// The trace of a created task (transaction.ts createTask, Pi 2ec55d80 "Durable and pi-ai telemetry"): an omitted one is the creating
// commit's carrier, `trace: "root"` stores none, an explicit one is kept, and a run's carriers come from its input submissions.
func TestCreateTaskTraceAndLinks(t *testing.T) {
	def := &session.TaskDef{Name: "test.trace", Version: 1, Initial: func(any) any { return jv.NewObject() }}
	carrier := func(id string) *jv.Object {
		o := jv.NewObject()
		o.Set("span", id)
		return o
	}
	mem := sessiontest.New()
	s := newSession(mem, mem.NextID())
	scope := session.TxScope{Span: 7, Trace: carrier("creator")}
	var ids [4]session.ID
	pub, err := s.Commit(func(tx *session.Tx) error {
		if _, err := tx.CreateRootConversation(); err != nil {
			return err
		}
		var err error
		opts := session.TaskOptions{ConversationID: 1}
		if ids[0], err = tx.CreateTask(def, nil, opts); err != nil {
			return err
		}
		root := opts
		root.NoTrace = true
		if ids[1], err = tx.CreateTask(def, nil, root); err != nil {
			return err
		}
		own := opts
		own.Trace, own.Links = carrier("own"), []any{carrier("link")}
		ids[2], err = tx.CreateTask(def, nil, own)
		return err
	}, scope)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Span != 7 {
		t.Fatalf("the publication names the commit's span: got %d", pub.Span)
	}
	var traces []string
	for _, change := range pub.Changes {
		if change.Table != session.WriteTask {
			continue
		}
		shown := func(key string) string {
			v, ok := change.Record.Get(key)
			if !ok {
				return "-"
			}
			return jv.Stringify(v)
		}
		traces = append(traces, shown("trace")+"|"+shown("links"))
	}
	want := []string{`{"span":"creator"}|-`, `-|-`, `{"span":"own"}|[{"span":"link"}]`}
	for i, w := range want {
		if i >= len(traces) || traces[i] != w {
			t.Errorf("task %d: got %v want %s", i, traces, w)
			break
		}
	}
}
