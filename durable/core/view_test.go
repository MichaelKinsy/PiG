package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

func parsed(t *testing.T, text string) *jv.Object {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jv.Object)
}

// viewHarness is a viewWatch with the value a host holds: advance applies the frame's ops to it, as the host does.
type viewHarness struct {
	*viewWatch
	value *jv.Object
}

func (v *viewHarness) advance(pub *session.Publication) ([]delta.Op, *jv.Object, error) {
	ops, changes := v.viewWatch.advance(pub)
	if len(ops) > 0 {
		next, err := delta.Apply(v.value, ops)
		if err != nil {
			return nil, nil, err
		}
		v.value = next.(*jv.Object)
	}
	return ops, changes, nil
}

func newView(t *testing.T) *viewHarness {
	value := parsed(t, `{"conversation":{"id":1},"entries":[{"id":3,"conversationId":1,"kind":"a"}],"docs":{"pi.live":{}}}`)
	list, _ := value.Get("entries")
	var slim []viewEntry
	for _, e := range list.([]any) {
		slim = append(slim, viewEntryOf(e.(*jv.Object)))
	}
	return &viewHarness{
		viewWatch: &viewWatch{conv: 1, entries: slim, docs: map[string]viewDoc{"pi.live": {id: 2, version: 1}}},
		value:     value,
	}
}

func entryChange(t *testing.T, text string) session.Change {
	return session.Change{Kind: session.ChangeTable, Table: session.WriteEntry, Record: parsed(t, text)}
}

// view.ts advance(): an entry without a head appends; a head marker replaces the entries before the first kept one; a
// document op of a mounted document is prefixed with its path; every op is applied to the value the watch holds.
func TestViewAdvanceMatchesViewTs(t *testing.T) {
	w := newView(t)
	ops, _, err := w.advance(&session.Publication{Changes: []session.Change{
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":2,"kind":"pi.live"}`), Version: 1, Value: parsed(t, `{"run":{"taskId":9}}`),
			Ops: []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("run")}, Value: parsed(t, `{"taskId":9}`)}}},
		entryChange(t, `{"id":4,"conversationId":1,"kind":"b"}`),
		entryChange(t, `{"id":5,"conversationId":2,"kind":"other conversation"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(delta.OpsValue(ops)); got != `[["s",["docs","pi.live","run"],{"taskId":9}],["p",["entries"],1,0,[{"id":4,"conversationId":1,"kind":"b"}]]]` {
		t.Fatalf("ops %s", got)
	}
	if got := jv.Stringify(w.value); got != `{"conversation":{"id":1},"entries":[{"id":3,"conversationId":1,"kind":"a"},{"id":4,"conversationId":1,"kind":"b"}],"docs":{"pi.live":{"run":{"taskId":9}}}}` {
		t.Fatalf("value %s", got)
	}
	// A head marker at 4 keeps the entries from 4 on, placed after the marker.
	ops, _, err = w.advance(&session.Publication{Changes: []session.Change{entryChange(t, `{"id":6,"conversationId":1,"kind":"pi.head","head":4}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(delta.OpsValue(ops)); got != `[["p",["entries"],0,1,[{"id":6,"conversationId":1,"kind":"pi.head","head":4}]]]` {
		t.Fatalf("head ops %s", got)
	}
	if got := jv.Stringify(objOf(w.value, "docs")); got != `{"pi.live":{"run":{"taskId":9}}}` {
		t.Fatalf("docs %s", got)
	}
}

// A retired mounted document is deleted from the view; a new incarnation or version replaces it whole; an unmounted
// document and a keyed one are ignored.
func TestViewAdvanceRetiresAndReplacesMountedDocuments(t *testing.T) {
	w := newView(t)
	ops, _, err := w.advance(&session.Publication{Changes: []session.Change{
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":9,"kind":"app.other"}`), Version: 1, Value: parsed(t, `{}`)},
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":10,"kind":"pi.live","key":"k"}`), Version: 1, Value: parsed(t, `{}`)},
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":11,"kind":"pi.live"}`), Version: 1, Value: parsed(t, `{"x":1}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(delta.OpsValue(ops)); got != `[["s",["docs","pi.live"],{"x":1}]]` {
		t.Fatalf("a new incarnation replaces the document whole: %s", got)
	}
	ops, _, _ = w.advance(&session.Publication{Changes: []session.Change{
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":99,"kind":"pi.live"}`), Version: 1, Value: nil},
	}})
	if len(ops) != 0 {
		t.Fatalf("retiring an incarnation the view does not hold changes nothing: %v", ops)
	}
	ops, _, _ = w.advance(&session.Publication{Changes: []session.Change{
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":11,"kind":"pi.live"}`), Version: 2, Value: parsed(t, `{"x":2}`),
			Ops: []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("x")}, Value: float64(2)}}},
	}})
	if got := jv.Stringify(delta.OpsValue(ops)); got != `[["s",["docs","pi.live"],{"x":2}]]` {
		t.Fatalf("a new definition version replaces the document whole: %s", got)
	}
	ops, _, _ = w.advance(&session.Publication{Changes: []session.Change{
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":11,"kind":"pi.live"}`), Version: 2, Value: nil},
	}})
	if got := jv.Stringify(delta.OpsValue(ops)); got != `[["d",["docs","pi.live"]]]` {
		t.Fatalf("a retired document is deleted: %s", got)
	}
}

// With changes, a frame carries the conversation's records of the commit.
func TestViewAdvanceCarriesTheCommitsRecordsWhenAsked(t *testing.T) {
	w := newView(t)
	w.changes = true
	_, changes, err := w.advance(&session.Publication{Changes: []session.Change{
		entryChange(t, `{"id":4,"conversationId":1,"kind":"b"}`),
		{Kind: session.ChangeTable, Table: session.WriteTask, Record: parsed(t, `{"id":7,"conversationId":1,"kind":"t"}`)},
		{Kind: session.ChangeTable, Table: session.WriteTask, Record: parsed(t, `{"id":8,"conversationId":2,"kind":"t"}`)},
		{Kind: session.ChangeTable, Table: session.WriteSubmission, Record: parsed(t, `{"id":9,"conversationId":1,"status":"queued"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := jv.Stringify(changes); got != `{"entries":[{"id":4,"conversationId":1,"kind":"b"}],"tasks":[{"id":7,"conversationId":1,"kind":"t"}],"submissions":[{"id":9,"conversationId":1,"status":"queued"}]}` {
		t.Fatalf("changes %s", got)
	}
}

// Session.subscribeCommits receives types.ts CommitChange records: a table write is {type, value}; a document change
// carries its record, conversation, version, value and ops, with a null value and no version for a retirement; a copy
// names its source.
func TestCommitChangesAreTheSessionPublicationShape(t *testing.T) {
	got := jv.Stringify(commitChanges(&session.Publication{Seq: 5, Changes: []session.Change{
		{Kind: session.ChangeTable, Table: session.WriteTask, Record: parsed(t, `{"id":7}`)},
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":2}`), Version: 1, Value: parsed(t, `{"a":1}`),
			Ops: []delta.Op{{Verb: delta.Set, Path: delta.Path{delta.Key("a")}, Value: 1.0}}},
		{Kind: session.ChangeDocument, ConversationID: 1, Record: parsed(t, `{"id":3}`), Version: 1},
		{Kind: session.ChangeDocumentCopy, ConversationID: 4, Record: parsed(t, `{"id":8}`), Source: session.DocCopySource{ID: 2, At: session.AsOf(6)}},
		{Kind: session.ChangeDocumentCopy, ConversationID: 4, Record: parsed(t, `{"id":9}`), Source: session.DocCopySource{ID: 3, At: session.Current}},
	}}))
	want := `[{"type":"task","value":{"id":7}},` +
		`{"type":"document","record":{"id":2},"conversationId":1,"version":1,"value":{"a":1},"ops":[["s",["a"],1]]},` +
		`{"type":"document","record":{"id":3},"conversationId":1,"value":null,"ops":[]},` +
		`{"type":"document.copy","record":{"id":8},"conversationId":4,"source":{"id":2,"at":6}},` +
		`{"type":"document.copy","record":{"id":9},"conversationId":4,"source":{"id":3,"at":"current"}}]`
	if got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
}

// Harness settings are live in Pi (resolveSettings(options.settings) at each read): a registry event that carries
// settings replaces the ones the core reads, and does not reload the registry.
func TestSettingsEventReplacesTheLiveSettings(t *testing.T) {
	h := &handler{open: parsed(t, `{"settings":{"followUpMode":"one"}}`)}
	if h.queueModes().FollowUp != "one" {
		t.Fatal("open's settings are the starting point")
	}
	if err := h.onRegistry(&abi.Event{Payload: []byte(`{"settings":{"followUpMode":"all"}}`)}); err != nil {
		t.Fatal(err)
	}
	if h.queueModes().FollowUp != "all" {
		t.Fatalf("follow-up mode %q after the settings event", h.queueModes().FollowUp)
	}
}
