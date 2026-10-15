// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package inbox_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// runs is the generation side of admission: Start records the placed inputs and takes pi.live.run, as startRun does.
type runs struct{ started [][]session.ID }

func (r *runs) Live(tx *session.Tx, conv session.ID) (*delta.Obj, error) {
	return tx.Doc(turn.LiveDef, session.DocArgs{Owner: conv})
}

func (r *runs) Start(_ *session.Tx, _ session.ID, live *delta.Obj, inputs []session.ID) error {
	r.started = append(r.started, inputs)
	run := jv.NewObject()
	run.Set("taskId", float64(99))
	list := make([]any, len(inputs))
	for i, id := range inputs {
		list[i] = float64(id)
	}
	run.Set("inputs", list)
	return live.Set("run", run)
}

type rig struct {
	t    *testing.T
	mem  *sessiontest.MemStore
	s    *session.Session
	runs *runs
	conv session.ID
}

var oneAtATime = inbox.QueueModes{Steering: inbox.ModeOne, FollowUp: inbox.ModeOne}

func newRig(t *testing.T) *rig {
	mem := sessiontest.New()
	s := session.New(mem, mem.NextID(), func() float64 { return 1000 })
	r := &rig{t: t, mem: mem, s: s, runs: &runs{}}
	r.commit(func(tx *session.Tx) error {
		c, err := tx.CreateRootConversation()
		r.conv = idOfRecord(c)
		return err
	})
	return r
}

func (r *rig) commit(fn func(tx *session.Tx) error) {
	r.t.Helper()
	if _, err := r.s.Commit(fn, session.TxScope{}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) admit(d inbox.Draft, modes inbox.QueueModes) (id session.ID, err error) {
	_, err = r.s.Commit(func(tx *session.Tx) error {
		var e error
		id, e = inbox.Admit(tx, r.runs, r.conv, d, 1000, modes)
		return e
	}, session.TxScope{})
	return id, err
}

func (r *rig) must(d inbox.Draft, modes inbox.QueueModes) session.ID {
	r.t.Helper()
	id, err := r.admit(d, modes)
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rig) submission(id session.ID) string {
	rec, _ := r.mem.Submission(id)
	return jv.Stringify(rec)
}

func (r *rig) items() string {
	v, err := r.s.Snapshot(inbox.Def, session.DocArgs{Owner: r.conv})
	if err != nil {
		r.t.Fatal(err)
	}
	items, _ := v.Get("items")
	return jv.Stringify(items)
}

func input(text string, whenBusy string) inbox.Draft {
	return inbox.Draft{Type: inbox.TypeInput, Content: text, WhenBusy: whenBusy}
}

// write is a write that affects model context, so it queues while busy: a write of only kind and data is appended at once.
func write(head any) inbox.Draft {
	e := jv.NewObject()
	e.Set("kind", "note")
	message := jv.NewObject()
	message.Set("role", "user")
	message.Set("content", "w")
	message.Set("timestamp", float64(0))
	e.Set("model", []any{message})
	if head != nil {
		e.Set("head", head)
	}
	return inbox.Draft{Type: inbox.TypeWrite, Entry: e}
}

func TestIdleInputPlacesAUserEntryAndStartsARun(t *testing.T) {
	r := newRig(t)
	id := r.must(input("hello", ""), oneAtATime)
	entryID := session.ID(0)
	if rec, _ := r.mem.Submission(id); rec != nil {
		e, _ := rec.Get("entry")
		entryID = session.ID(e.(float64))
	}
	if got := r.submission(id); got != `{"conversationId":1,"type":"input","status":"placed","entry":`+itoa(entryID)+`,"id":`+itoa(id)+`}` {
		t.Fatal(got)
	}
	if len(r.runs.started) != 1 || len(r.runs.started[0]) != 1 || r.runs.started[0][0] != id {
		t.Fatalf("run started with %v", r.runs.started)
	}
	entry, _, _ := r.mem.Entry(entryID)
	// pi-durable's typed appendEntry writes {...draft, kind} (session/transaction.ts), so kind follows model (E11 capture).
	if got := jv.Stringify(entry); got != `{"model":[{"role":"user","content":"hello","timestamp":1000}],"kind":"pi.user","id":`+itoa(entryID)+`,"conversationId":1}` {
		t.Fatal(got)
	}
}

func TestBusyConversationQueuesOrRejects(t *testing.T) {
	r := newRig(t)
	r.must(input("first", ""), oneAtATime)
	follow := r.must(input("second", ""), oneAtATime)
	steer := r.must(input("third", "steer"), oneAtATime)
	if got := r.items(); got != `[{"id":`+itoa(follow)+`,"mode":"followUp","content":"second"},{"id":`+itoa(steer)+`,"mode":"steer","content":"third"}]` {
		t.Fatal(got)
	}
	if got := r.submission(follow); got != `{"conversationId":1,"type":"input","status":"queued","id":`+itoa(follow)+`}` {
		t.Fatal(got)
	}
	_, err := r.admit(input("no", "reject"), oneAtATime)
	var busy *inbox.BusyError
	if !errors.As(err, &busy) || !errors.Is(err, inbox.ErrConversationBusy) || err.Error() != "Conversation 1 is busy" {
		t.Fatalf("want ConversationBusy, got %v", err)
	}
	if len(r.runs.started) != 1 {
		t.Fatal("a queued submission must not start a run")
	}
}

func itoa(id session.ID) string { return jv.FormatNumber(float64(id)) }

func TestRequestIDReturnsTheExistingSubmissionAndChecksItsType(t *testing.T) {
	r := newRig(t)
	rid := "req-1"
	d := input("a", "")
	d.RequestID = &rid
	id := r.must(d, oneAtATime)
	before := r.mem.NextID()
	if again := r.must(d, oneAtATime); again != id || r.mem.NextID() != before {
		t.Fatalf("the same request must not write: %d vs %d", again, id)
	}
	w := write(nil)
	w.RequestID = &rid
	if _, err := r.admit(w, oneAtATime); err == nil || err.Error() != "Request req-1 already identifies a submission of type input" {
		t.Fatalf("got %v", err)
	}
}

func TestIdleWriteAppendsAndSettlesDoneOrStale(t *testing.T) {
	r := newRig(t)
	id := r.must(write(nil), oneAtATime)
	if got := r.submission(id); got != `{"conversationId":1,"type":"write","status":"done","entry":`+entryOf(r, id)+`,"id":`+itoa(id)+`}` {
		t.Fatal(got)
	}
	// A head marker at entry 2 makes a head before it stale.
	r.commit(func(tx *session.Tx) error { _, err := tx.AppendEntry(r.conv, headDraft(2)); return err })
	stale := r.must(write(float64(1)), oneAtATime)
	if got := r.submission(stale); got != `{"conversationId":1,"type":"write","status":"unanswered","reason":"stale","id":`+itoa(stale)+`}` {
		t.Fatal(got)
	}
}

func headDraft(head float64) *jv.Object {
	e := jv.NewObject()
	e.Set("kind", "pi.head")
	e.Set("head", head)
	return e
}

func TestBoundaryPlacesWritesFirstThenOneUserItemPerMode(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	f1 := r.must(input("f1", ""), oneAtATime)
	f2 := r.must(input("f2", ""), oneAtATime)
	s1 := r.must(input("s1", "steer"), oneAtATime)
	w := r.must(write(nil), oneAtATime)
	// A postTools boundary places the write and the first steer; follow-ups wait for final.
	var res inbox.Result
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, oneAtATime)
		if err != nil {
			return err
		}
		res, err = inbox.Apply(tx, b, false, 1000)
		return err
	})
	if len(res.Users) != 1 || res.Users[0] != s1 || res.Reset {
		t.Fatalf("postTools placed %v", res)
	}
	if got := r.submission(w); got != `{"conversationId":1,"type":"write","status":"done","id":`+itoa(w)+`,"entry":`+entryOf(r, w)+`}` {
		t.Fatal(got)
	}
	// Final with mode all places every follow-up in ID order.
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, inbox.QueueModes{Steering: inbox.ModeAll, FollowUp: inbox.ModeAll})
		if err != nil {
			return err
		}
		res, err = inbox.Apply(tx, b, true, 1000)
		return err
	})
	if len(res.Users) != 2 || res.Users[0] != f1 || res.Users[1] != f2 {
		t.Fatalf("final placed %v", res.Users)
	}
	if got := r.items(); got != `[]` {
		t.Fatalf("inbox %s", got)
	}
}

func entryOf(r *rig, id session.ID) string {
	rec, _ := r.mem.Submission(id)
	e, _ := rec.Get("entry")
	return jv.FormatNumber(e.(float64))
}

func TestAResetWriteTurnsPostToolsIntoFinalAndStaleWritesAreSettled(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	f1 := r.must(input("f1", ""), oneAtATime)
	reset := r.must(write("self"), oneAtATime)
	var res inbox.Result
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, oneAtATime)
		if err != nil {
			return err
		}
		res, err = inbox.Apply(tx, b, false, 1000)
		return err
	})
	if !res.Reset || len(res.Users) != 1 || res.Users[0] != f1 {
		t.Fatalf("reset boundary placed %+v", res)
	}
	// The reset wrote a head marker at its own entry: a later write reaching before it is stale in the next boundary.
	if got := r.submission(reset); got != `{"conversationId":1,"type":"write","status":"done","id":`+itoa(reset)+`,"entry":`+entryOf(r, reset)+`}` {
		t.Fatal(got)
	}
}

func TestAbortReportsEachState(t *testing.T) {
	r := newRig(t)
	placed := r.must(input("run", ""), oneAtATime)
	queued := r.must(input("later", ""), oneAtATime)
	abort := func(id, conv session.ID) (res inbox.AbortResult) {
		r.commit(func(tx *session.Tx) (err error) { res, err = inbox.Abort(tx, id, conv); return err })
		return res
	}
	if got := abort(placed, 0); got != inbox.AlreadyPlaced {
		t.Fatal(got)
	}
	if got := abort(queued, 7); got != inbox.NotFound {
		t.Fatalf("another conversation: %s", got)
	}
	if got := abort(queued, r.conv); got != inbox.Aborted {
		t.Fatal(got)
	}
	if got := r.submission(queued); got != `{"conversationId":1,"type":"input","status":"unanswered","id":`+itoa(queued)+`,"reason":"aborted"}` {
		t.Fatal(got)
	}
	if r.items() != `[]` {
		t.Fatal(r.items())
	}
	if got := abort(queued, 0); got != inbox.Settled {
		t.Fatal(got)
	}
	if got := abort(9999, 0); got != inbox.NotFound {
		t.Fatal(got)
	}
}

func TestWithdrawQueuedKeepsWrites(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	a := r.must(input("a", ""), oneAtATime)
	w := r.must(write(nil), oneAtATime)
	b := r.must(input("b", "steer"), oneAtATime)
	r.commit(func(tx *session.Tx) error { return inbox.WithdrawQueued(tx, r.conv) })
	if got := r.items(); got != `[{"id":`+itoa(w)+`,"mode":"write","entry":{"kind":"note","model":[{"role":"user","content":"w","timestamp":0}]}}]` {
		t.Fatal(got)
	}
	for _, id := range []session.ID{a, b} {
		if got := r.submission(id); !contains(got, `"status":"unanswered","id":`+itoa(id)+`,"reason":"aborted"`) {
			t.Fatal(got)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func idOfRecord(o *jv.Object) session.ID {
	id, _ := o.Get("id")
	return session.ID(id.(float64))
}

func (r *rig) boundary(modes inbox.QueueModes, final bool) (res inbox.Result) {
	r.t.Helper()
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, modes)
		if err != nil {
			return err
		}
		res, err = inbox.Apply(tx, b, final, 1000)
		return err
	})
	return res
}

func TestOneAtATimePlacesOneSteerAndAllModePlacesEveryOne(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	s1 := r.must(input("s1", "steer"), oneAtATime)
	s2 := r.must(input("s2", "steer"), oneAtATime)
	if res := r.boundary(oneAtATime, false); len(res.Users) != 1 || res.Users[0] != s1 {
		t.Fatalf("one-at-a-time placed %v", res.Users)
	}
	r2 := newRig(t)
	r2.must(input("run", ""), oneAtATime)
	a := r2.must(input("s1", "steer"), oneAtATime)
	b := r2.must(input("s2", "steer"), oneAtATime)
	if res := r2.boundary(inbox.QueueModes{Steering: inbox.ModeAll, FollowUp: inbox.ModeOne}, false); len(res.Users) != 2 || res.Users[0] != a || res.Users[1] != b {
		t.Fatalf("all placed %v", res.Users)
	}
	_ = s2
}

func TestUserItemsArePlacedInQueueOrderAcrossSteersAndFollowUps(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	f := r.must(input("f", ""), oneAtATime)
	s := r.must(input("s", "steer"), oneAtATime)
	if res := r.boundary(oneAtATime, true); len(res.Users) != 2 || res.Users[0] != f || res.Users[1] != s {
		t.Fatalf("placed %v, want the follow-up queued first then the steer", res.Users)
	}
}

func TestABoundarySettlesAQueuedWriteWhoseHeadIsBeforeTheActiveRangeStale(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	old := r.must(write(float64(1)), oneAtATime)
	r.commit(func(tx *session.Tx) error { _, err := tx.AppendEntry(r.conv, headDraft(2)); return err })
	r.boundary(oneAtATime, false)
	if got := r.submission(old); got != `{"conversationId":1,"type":"write","status":"unanswered","id":`+itoa(old)+`,"reason":"stale"}` {
		t.Fatal(got)
	}
	if r.items() != `[]` {
		t.Fatalf("a stale item leaves the inbox: %s", r.items())
	}
}

func TestAnIdleConversationWithQueuedItemsQueuesBehindThemAndRunsAFinalBoundary(t *testing.T) {
	r := newRig(t)
	first := r.must(input("first", ""), oneAtATime)
	// A run ended with an item left queued: the conversation is idle and the inbox is not empty.
	queued := r.must(input("queued", ""), oneAtATime)
	r.commit(func(tx *session.Tx) error {
		live, err := r.runs.Live(tx, r.conv)
		if err != nil {
			return err
		}
		live.Delete("run")
		return nil
	})
	r.runs.started = nil
	next := r.must(input("next", ""), oneAtATime)
	if len(r.runs.started) != 1 || len(r.runs.started[0]) != 1 || r.runs.started[0][0] != queued {
		t.Fatalf("the final boundary starts a run for the oldest follow-up: %v", r.runs.started)
	}
	if got := r.items(); got != `[{"id":`+itoa(next)+`,"mode":"followUp","content":"next"}]` {
		t.Fatalf("%s (first %d)", got, first)
	}
}

func parsed(text string) *jv.Object {
	v, err := jv.Parse([]byte(text))
	if err != nil {
		panic(err)
	}
	return v.(*jv.Object)
}

func inputEntry(kind any, data any) *jv.Object {
	e := jv.NewObject()
	e.Set("kind", kind)
	if data != nil {
		e.Set("data", data)
	}
	return e
}

func inputAs(text string, entry *jv.Object, whenBusy string) inbox.Draft {
	d := input(text, whenBusy)
	d.Entry = entry
	return d
}

func dataWrite(data string) inbox.Draft {
	e := jv.NewObject()
	e.Set("kind", "chat.message")
	e.Set("data", data)
	return inbox.Draft{Type: inbox.TypeWrite, Entry: e}
}

func entryJSON(r *rig, id session.ID) string {
	rec, _ := r.mem.Submission(id)
	e, _ := rec.Get("entry")
	entry, _, _ := r.mem.Entry(session.ID(e.(float64)))
	return jv.Stringify(entry)
}

// harness-input-entries.test.ts "places an idle input as its entry": the kind and data of the input's entry, then the user
// message of its content as the model ({...pickInputEntry(entry), model}).
func TestAnIdleInputWithAnEntryIsPlacedAsThatEntry(t *testing.T) {
	r := newRig(t)
	id := r.must(inputAs("ann: hi", inputEntry("chat.mention", parsed(`{"author":"ann"}`)), ""), oneAtATime)
	want := `{"kind":"chat.mention","data":{"author":"ann"},"model":[{"role":"user","content":"ann: hi","timestamp":1000}],"id":` + entryOf(r, id) + `,"conversationId":1}`
	if got := entryJSON(r, id); got != want {
		t.Fatalf("entry %s, want %s", got, want)
	}
	if len(r.runs.started) != 1 || r.runs.started[0][0] != id {
		t.Fatalf("run started with %v", r.runs.started)
	}
	// An entry without data places without a data member.
	id = r.must(inputAs("bare", inputEntry("p2.input", nil), "steer"), oneAtATime)
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, oneAtATime)
		if err != nil {
			return err
		}
		_, err = inbox.Apply(tx, b, false, 1000)
		return err
	})
	if got := entryJSON(r, id); !contains(got, `{"kind":"p2.input","model":[{"role":"user","content":"bare","timestamp":1000}]`) {
		t.Fatalf("entry %s", got)
	}
}

// harness-input-entries.test.ts "takes only kind and data" and "queues busy inputs with their entries": a queued input
// carries its entry's kind and data, nothing else, and a boundary places it as that entry.
func TestAQueuedInputCarriesItsEntryAndABoundaryPlacesItAsThatEntry(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	extra := inputEntry("chat.mention", nil)
	forged := jv.NewObject()
	forged.Set("role", "user")
	extra.Set("model", []any{forged})
	extra.Set("head", "self")
	extra.Set("edits", []any{})
	steer := r.must(inputAs("s", extra, "steer"), oneAtATime)
	follow := r.must(inputAs("f", inputEntry("p2.input", parsed(`{"n":1}`)), ""), oneAtATime)
	want := `[{"id":` + itoa(steer) + `,"mode":"steer","content":"s","entry":{"kind":"chat.mention"}},{"id":` + itoa(follow) + `,"mode":"followUp","content":"f","entry":{"kind":"p2.input","data":{"n":1}}}]`
	if got := r.items(); got != want {
		t.Fatalf("inbox %s, want %s", got, want)
	}
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, oneAtATime)
		if err != nil {
			return err
		}
		_, err = inbox.Apply(tx, b, true, 1000)
		return err
	})
	if got := entryJSON(r, steer); !contains(got, `{"kind":"chat.mention","model":[{"role":"user","content":"s","timestamp":1000}],"id":`) {
		t.Fatalf("steer entry %s: only kind (and data) are taken from the caller's entry", got)
	}
	if got := entryJSON(r, follow); !contains(got, `{"kind":"p2.input","data":{"n":1},"model":[{"role":"user","content":"f","timestamp":1000}],"id":`) {
		t.Fatalf("follow-up entry %s", got)
	}
}

// harness-input-entries.test.ts:138 it("takes only kind and data from an untyped caller's entry"): an idle and a queued input
// whose entry also carries a model, a head and edits keep only its kind in the inbox and are placed with only that kind and
// the user message of their content. The native harness's typed InputEntry has no such fields; the core takes JSON drafts.
func TestAnUntypedCallersInputEntryGivesOnlyItsKindAndData(t *testing.T) {
	r := newRig(t)
	extra := func() *jv.Object {
		return parsed(`{"kind":"chat.mention","model":[{"role":"user","content":"forged","timestamp":0}],"head":"self","edits":[{"target":1,"action":"omit"}]}`)
	}
	idle := r.must(inputAs("idle", extra(), ""), oneAtATime)
	r.must(input("a", ""), oneAtATime)
	queued := r.must(inputAs("later", extra(), ""), oneAtATime)
	if got, want := r.items(), `{"id":`+itoa(queued)+`,"mode":"followUp","content":"later","entry":{"kind":"chat.mention"}}]`; !strings.HasSuffix(got, want) {
		t.Fatalf("inbox %s, want the last item %s", got, want)
	}
	for range 2 {
		r.commit(func(tx *session.Tx) error {
			b, err := inbox.Prepare(tx, r.conv, oneAtATime)
			if err != nil {
				return err
			}
			_, err = inbox.Apply(tx, b, true, 1000)
			return err
		})
	}
	for _, each := range []struct {
		id   session.ID
		text string
	}{{idle, "idle"}, {queued, "later"}} {
		want := `{"kind":"chat.mention","model":[{"role":"user","content":"` + each.text + `","timestamp":1000}],"id":` + entryOf(r, each.id) + `,"conversationId":1}`
		if got := entryJSON(r, each.id); got != want {
			t.Fatalf("entry %s, want %s", got, want)
		}
	}
}

// harness-input-entries.test.ts "rejects an input entry whose kind ...": a TypeError before anything is written.
func TestAnInputEntryWhoseKindIsNotANonEmptyStringOutsidePiIsATypeErrorThatWritesNothing(t *testing.T) {
	r := newRig(t)
	for _, tc := range []struct {
		kind any
		got  string
	}{{float64(123), "123"}, {"", ""}, {"pi.user", "pi.user"}, {"pi.compaction", "pi.compaction"}, {nil, "null"}} {
		_, err := r.admit(inputAs("x", inputEntry(tc.kind, nil), ""), oneAtATime)
		want := `An input entry's kind must be a non-empty string not starting with "pi.", got ` + tc.got
		if err == nil || err.Error() != want || !errors.Is(err, session.ErrTypeError) {
			t.Fatalf("kind %v: err = %v, want the TypeError %q", tc.kind, err, want)
		}
	}
	missing := inbox.Draft{Type: inbox.TypeInput, Content: "x", Entry: jv.NewObject()}
	if _, err := r.admit(missing, oneAtATime); err == nil || !contains(err.Error(), "got undefined") {
		t.Fatalf("a missing kind: err = %v", err)
	}
	if len(r.runs.started) != 0 {
		t.Fatalf("started %v: a rejected input wrote something", r.runs.started)
	}
	// The kind is checked before a known request ID returns its submission (submissions.ts admitSubmission).
	known := dataWrite("n")
	known.RequestID = new("r-known")
	r.must(known, oneAtATime)
	again := inputAs("x", inputEntry("pi.user", nil), "")
	again.RequestID = new("r-known")
	if _, err := r.admit(again, oneAtATime); !errors.Is(err, session.ErrTypeError) {
		t.Fatalf("a known request ID with a pi. kind: err = %v, want the TypeError", err)
	}
	// Writes keep their kinds.
	r.must(write("self"), oneAtATime)
}

// harness-input-entries.test.ts "data-only writes": a write of only kind and data is appended and settled done at once,
// busy or not, ahead of queued items; one with a model, head or edits (even empty) still queues; only a queued reset holds
// a data-only write back, and once the reset is placed they go in at once again.
func TestADataOnlyWriteIsAppendedAtOnceExceptBehindAQueuedReset(t *testing.T) {
	r := newRig(t)
	r.must(input("run", ""), oneAtATime)
	follow := r.must(input("f", ""), oneAtATime)
	note := r.must(dataWrite("n"), oneAtATime)
	if got := r.submission(note); !contains(got, `"type":"write","status":"done"`) {
		t.Fatalf("note %s, want done at once", got)
	}
	if got := r.items(); got != `[{"id":`+itoa(follow)+`,"mode":"followUp","content":"f"}]` {
		t.Fatalf("inbox %s: a data-only write must not queue", got)
	}
	empty := dataWrite("e")
	empty.Entry.Set("model", []any{})
	queuedEmpty := r.must(empty, oneAtATime)
	if got := r.submission(queuedEmpty); !contains(got, `"status":"queued"`) {
		t.Fatalf("a write with an empty model is %s, want queued", got)
	}
	reset := r.must(write("self"), oneAtATime)
	held := r.must(dataWrite("after"), oneAtATime)
	if got := r.submission(held); !contains(got, `"status":"queued"`) {
		t.Fatalf("a data-only write behind a queued reset is %s, want queued", got)
	}
	r.commit(func(tx *session.Tx) error {
		b, err := inbox.Prepare(tx, r.conv, oneAtATime)
		if err != nil {
			return err
		}
		_, err = inbox.Apply(tx, b, false, 1000)
		return err
	})
	if got := r.submission(held); !contains(got, `"status":"done"`) || r.submission(reset) == "" {
		t.Fatalf("held %s", got)
	}
	later := r.must(dataWrite("again"), oneAtATime)
	if got := r.submission(later); !contains(got, `"status":"done"`) {
		t.Fatalf("once the reset is placed a data-only write is %s, want done at once", got)
	}
}

func TestIsDisplayOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  map[string]any
		want bool
	}{
		{"kind only", nil, true},
		{"data", map[string]any{"data": "x"}, true},
		{"model", map[string]any{"model": []any{}}, false},
		{"head", map[string]any{"head": float64(1)}, false},
		{"self", map[string]any{"head": "self"}, false},
		{"edits", map[string]any{"edits": []any{}}, false},
	} {
		e := jv.NewObject()
		e.Set("kind", "k")
		for key, value := range tc.set {
			e.Set(key, value)
		}
		if got := inbox.IsDisplayOnly(e); got != tc.want {
			t.Errorf("%s: IsDisplayOnly = %t, want %t", tc.name, got, tc.want)
		}
	}
}
