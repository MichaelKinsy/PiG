package turn_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

func parse(t *testing.T, text string) any {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newSession(t *testing.T) *session.Session {
	t.Helper()
	mem := sessiontest.New()
	s := session.New(mem, mem.NextID(), func() float64 { return 1000 })
	if _, err := s.Commit(func(tx *session.Tx) error { _, err := tx.CreateRootConversation(); return err }, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	return s
}

func liveValue(t *testing.T, s *session.Session) string {
	t.Helper()
	var out string
	if _, err := s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err == nil {
			out = jv.Stringify(live.Snapshot())
		}
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestLiveRunLifecycle follows live.ts: endRun settles the run's inputs and clears run, generation and tools; a tool slot
// finishes with its result entry and loses its running output; compaction statuses are removed with their list.
func TestLiveRunLifecycle(t *testing.T) {
	s := newSession(t)
	var inputs []float64
	pub, err := s.Commit(func(tx *session.Tx) error {
		for range 2 {
			sub, err := tx.CreateSubmission(parse(t, `{"conversationId":1,"type":"user","status":"queued","requestId":"r"}`).(*jv.Object))
			if err != nil {
				return err
			}
			id, _ := sub.Get("id")
			inputs = append(inputs, id.(float64))
		}
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		if err := live.Set("run", parse(t, `{"taskId":7,"inputs":[`+jv.Stringify(inputs[0])+`,`+jv.Stringify(inputs[1])+`]}`)); err != nil {
			return err
		}
		if err := live.Set("generation", parse(t, `{"attempt":1}`)); err != nil {
			return err
		}
		return live.Set("tools", parse(t, `[{"callId":"a","name":"read","taskId":8,"status":"running","output":"x","droppedBytes":3},{"callId":"b","name":"bash","status":"pending"}]`))
	}, session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	_ = pub

	// finishSlot: done, entry kept, progress removed.
	if _, err := s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		slot := turn.ToolSlotOf(live, 8)
		if slot == nil || turn.ToolSlotOf(live, 99) != nil {
			t.Fatalf("slot lookup: %v", slot)
		}
		if err := turn.FinishSlot(slot); err != nil {
			return err
		}
		return slot.Set("entry", float64(41))
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if got, want := liveValue(t, s), `{"run":{"taskId":7,"inputs":[`+jv.Stringify(inputs[0])+`,`+jv.Stringify(inputs[1])+`]},"generation":{"attempt":1},"tools":[{"callId":"a","name":"read","taskId":8,"status":"done","entry":41},{"callId":"b","name":"bash","status":"pending"}]}`; got != want {
		t.Fatalf("after finishSlot:\n got %s\nwant %s", got, want)
	}

	// endRun of another task leaves run in place but still removes generation and tools.
	settle := turn.Unanswered("aborted")
	pub, err = s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		return turn.EndRun(tx, live, 99, settle)
	}, session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range pub.Changes {
		if c.Kind == session.ChangeTable && c.Table == session.WriteSubmission {
			t.Fatalf("a run of another task settled an input: %+v", c)
		}
	}
	if got, want := liveValue(t, s), `{"run":{"taskId":7,"inputs":[`+jv.Stringify(inputs[0])+`,`+jv.Stringify(inputs[1])+`]}}`; got != want {
		t.Fatalf("after a foreign endRun:\n got %s\nwant %s", got, want)
	}

	// endRun of the owner settles both inputs, in order.
	pub, err = s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		return turn.EndRun(tx, live, 7, settle)
	}, session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
	var settled []float64
	for _, c := range pub.Changes {
		if c.Kind == session.ChangeTable && c.Table == session.WriteSubmission {
			id, _ := c.Record.Get("id")
			settled = append(settled, id.(float64))
		}
	}
	if len(settled) != 2 || settled[0] != inputs[0] || settled[1] != inputs[1] {
		t.Fatalf("settled %v, want %v", settled, inputs)
	}
	// A run that ended without an answer records why.
	failure := `{"runFailure":{"taskId":7,"inputs":[` + jv.Stringify(inputs[0]) + `,` + jv.Stringify(inputs[1]) + `],"reason":"aborted"}`
	if got, want := liveValue(t, s), failure+`}`; got != want {
		t.Fatalf("after endRun:\n got %s\nwant %s", got, want)
	}

	// Compaction statuses: appended in order, found by task, removed with the list once empty.
	if _, err := s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		for _, id := range []string{"3", "5"} {
			if err := turn.AddCompactionStatus(live, parse(t, `{"taskId":`+id+`,"trigger":"threshold","blocking":false,"attempt":1}`).(*jv.Object)); err != nil {
				return err
			}
		}
		if turn.CompactionStatusOf(live, 5) == nil || turn.CompactionStatusOf(live, 4) != nil {
			t.Fatal("compaction status lookup")
		}
		return turn.RemoveCompactionStatus(live, 3)
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if got, want := liveValue(t, s), failure+`,"compactions":[{"taskId":5,"trigger":"threshold","blocking":false,"attempt":1}]}`; got != want {
		t.Fatalf("compactions:\n got %s\nwant %s", got, want)
	}
	if _, err := s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		return turn.RemoveCompactionStatus(live, 5)
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if got, want := liveValue(t, s), failure+`}`; got != want {
		t.Fatalf("emptied compactions: %s", got)
	}
}

// TestLiveCheckpointRule: a complete base whenever nothing runs, no generation and no running tool slot (live.ts).
func TestLiveCheckpointRule(t *testing.T) {
	for text, want := range map[string]bool{
		`{}`:                                true,
		`{"run":{"taskId":1,"inputs":[2]}}`: true,
		`{"generation":{"attempt":1}}`:      false,
		`{"tools":[{"status":"done"},{"status":"pending"}]}`:            true,
		`{"tools":[{"status":"done"},{"status":"running"}]}`:            false,
		`{"tools":[{"status":"running"}],"generation":{"attempt":1}}`:   false,
		`{"compactions":[{"taskId":1}],"tools":[{"status":"pending"}]}`: true,
	} {
		if got := turn.LiveDef.CheckpointWhen(parse(t, text).(*jv.Object), nil, 0); got != want {
			t.Errorf("checkpointWhen(%s) = %v, want %v", text, got, want)
		}
	}
}

// TestLiveVersion2 follows harness-compaction.test.ts "pi.live version 2": a version 1 value reads with each compaction's
// `reason` as `trigger`, keys in order, and is stored migrated on its next write.
func TestLiveVersion2(t *testing.T) {
	mem := sessiontest.New()
	clock := func() float64 { return 1000 }
	liveV1 := &session.DocDef{
		Kind: "pi.live", Version: 1, Scope: session.ScopeConversation, History: session.HistoryLatest, Fork: session.ForkInitial,
		Initial:        func(any) *jv.Object { return jv.NewObject() },
		CheckpointWhen: func(*jv.Object, []delta.Op, int) bool { return false },
	}
	old := session.New(mem, mem.NextID(), clock)
	if _, err := old.Commit(func(tx *session.Tx) error {
		if _, err := tx.CreateRootConversation(); err != nil {
			return err
		}
		live, err := tx.Doc(liveV1, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		if err := live.Set("compactions", parse(t, `[{"taskId":7,"reason":"threshold","blocking":true,"attempt":2}]`)); err != nil {
			return err
		}
		return live.Set("compactionFailure", parse(t, `{"taskId":5,"reason":"manual","error":"no model"}`))
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	stored := func() *session.StoredDoc {
		t.Helper()
		record, err := mem.FindDocument(session.Address{Kind: "pi.live", Scope: session.Scope{Kind: session.ScopeConversation, Owner: 1}}, session.Current)
		if err != nil || record == nil {
			t.Fatalf("find pi.live: %v %v", record, err)
		}
		id, _ := record.Get("id")
		doc, err := mem.Document(session.ID(id.(float64)), session.Current)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	s := session.New(mem, mem.NextID(), clock)
	const migrated = `{"compactions":[{"taskId":7,"trigger":"threshold","blocking":true,"attempt":2}],"compactionFailure":{"taskId":5,"trigger":"manual","error":"no model"}}`
	read, err := s.Snapshot(turn.LiveDef, session.DocArgs{Owner: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(read); got != migrated {
		t.Fatalf("read = %s, want %s", got, migrated)
	}
	if doc := stored(); doc.Version != 1 || !strings.Contains(jv.Stringify(doc.Value), `"reason":"threshold"`) {
		t.Fatalf("a read must leave the version 1 value stored: %d %s", doc.Version, jv.Stringify(doc.Value))
	}
	if _, err := s.Commit(func(tx *session.Tx) error {
		live, err := tx.Doc(turn.LiveDef, session.DocArgs{Owner: 1})
		if err != nil {
			return err
		}
		return live.Arr("compactions").Obj(0).Set("attempt", 3.0)
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if doc := stored(); doc.Version != 2 || !strings.Contains(jv.Stringify(doc.Value), `"trigger":"threshold","blocking":true,"attempt":3`) || !strings.Contains(jv.Stringify(doc.Value), `"compactionFailure":{"taskId":5,"trigger":"manual"`) {
		t.Fatalf("stored after write = %d %s", doc.Version, jv.Stringify(doc.Value))
	}
}
