// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package rec

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func obj(kv ...any) *jv.Object {
	o := jv.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func TestNewTaskKeyOrder(t *testing.T) {
	got := string(NewTask(7, 1, "pi.tool", 3, obj("a", 1.0), 0, false, obj("phase", "call")).Append(nil))
	want := `{"id":7,"conversationId":1,"kind":"pi.tool","version":3,"input":{"a":1},"background":false,"abortRequested":false,"state":{"status":"pending","checkpoint":{"phase":"call"}}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	got = string(NewTask(8, 1, "k", 1, nil, 5, true, nil).Append(nil))
	want = `{"id":8,"conversationId":1,"kind":"k","version":1,"input":null,"owner":5,"background":true,"abortRequested":false,"state":{"status":"pending","checkpoint":null}}`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A record read from a store and written back keeps unknown fields and their order, the way an object spread does.
func TestTaskCarriesUnknownFields(t *testing.T) {
	in := `{"id":3,"future":{"x":[1,2]},"conversationId":1,"kind":"k","version":1,"input":null,"background":false,"abortRequested":false,"memos":{"m":1},"state":{"status":"running","checkpoint":{"p":1},"extra":true}}`
	task, err := ParseTask([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(task.Append(nil)) != in {
		t.Fatalf("round trip changed the record: %s", task.Append(nil))
	}
	if task.Status() != Running || task.ID() != 3 || task.Kind() != "k" {
		t.Fatalf("accessors: %v %d %s", task.Status(), task.ID(), task.Kind())
	}
	next := task.WithState(obj("status", "completing", "outcome", 1.0))
	if string(next.Append(nil)) != `{"id":3,"future":{"x":[1,2]},"conversationId":1,"kind":"k","version":1,"input":null,"background":false,"abortRequested":false,"state":{"status":"completing","outcome":1}}` {
		t.Fatalf("completing drops memos: %s", next.Append(nil))
	}
	if string(task.Append(nil)) != in {
		t.Fatal("WithState changed its receiver")
	}
}

func TestParseTaskRejectsMalformed(t *testing.T) {
	for _, in := range []string{`[]`, `{}`, `{"id":1}`, `{"id":1,"conversationId":1,"kind":"k"}`,
		`{"id":1,"conversationId":1,"kind":"k","state":{"status":"zombie"}}`, `{"id":1.5,"conversationId":1,"kind":"k","state":{"status":"pending"}}`} {
		if _, err := ParseTask([]byte(in)); err == nil {
			t.Errorf("ParseTask(%s) succeeded", in)
		}
	}
}

// StampTimes is Transaction.#stampTimes: startedAt on the first change to running, endedAt on terminal, both carried.
func TestStampTimes(t *testing.T) {
	mk := func(status string, kv ...any) *Task {
		o := obj("id", 1.0, "conversationId", 1.0, "kind", "k", "version", 1.0, "input", nil, "background", false, "abortRequested", false)
		for i := 0; i+1 < len(kv); i += 2 {
			o.Set(kv[i].(string), kv[i+1])
		}
		o.Set("state", obj("status", status))
		return &Task{V: o}
	}
	cases := []struct {
		name      string
		value     *Task
		candidate *Task
		want      string
		same      bool
	}{
		{"pending is not stamped", mk("pending"), nil, "", true},
		{"first running", mk("running"), nil, `"startedAt":50`, false},
		{"running keeps its start", mk("running", "startedAt", 10.0), nil, "", true},
		{"terminal after running", mk("terminal", "startedAt", 10.0), nil, `"startedAt":10,"state":{"status":"terminal"},"endedAt":50`, false},
		{"terminal without a start", mk("terminal"), nil, `"state":{"status":"terminal"},"endedAt":50`, false},
		{"candidate start carries over", mk("running"), mk("running", "startedAt", 7.0), `"startedAt":7`, false},
		{"candidate end carries over", mk("terminal", "startedAt", 10.0), mk("terminal", "startedAt", 10.0, "endedAt", 9.0), `"endedAt":9`, false},
		{"settled record is kept", mk("terminal", "startedAt", 10.0, "endedAt", 20.0), nil, "", true},
	}
	for _, c := range cases {
		got := c.value.StampTimes(c.candidate, 50)
		if c.same {
			if got != c.value {
				t.Errorf("%s: got a new record %s", c.name, got.Append(nil))
			}
			continue
		}
		if !strings.Contains(string(got.Append(nil)), c.want) {
			t.Errorf("%s: %s does not contain %s", c.name, got.Append(nil), c.want)
		}
	}
	// Pi appends startedAt and endedAt after the existing keys, in that order.
	got := string(mk("terminal").StampTimes(nil, 50).Append(nil))
	if !strings.HasSuffix(got, `"state":{"status":"terminal"},"endedAt":50}`) {
		t.Errorf("key order: %s", got)
	}
}

func TestConversationColumns(t *testing.T) {
	c := NewConversation(5, 1, 9, 2, 7)
	if string(c.Append(nil)) != `{"id":5,"parent":{"conversationId":1,"at":9},"owner":{"conversationId":2,"taskId":7}}` {
		t.Fatalf("%s", c.Append(nil))
	}
	if v, ok := c.OwnerConversation(); !ok || v != 2 {
		t.Fatal("owner conversation")
	}
	if v, ok := c.OwnerTask(); !ok || v != 7 {
		t.Fatal("owner task")
	}
	root := NewConversation(1, 0, 0, 0, 0)
	if string(root.Append(nil)) != `{"id":1}` {
		t.Fatalf("%s", root.Append(nil))
	}
	if _, ok := root.OwnerConversation(); ok {
		t.Fatal("a root has no owner")
	}
	if _, _, ok := root.Parent(); ok {
		t.Fatal("a root has no parent")
	}
	// An owner object missing a field binds NULL for that column only (`owner?.taskId ?? null`).
	odd, err := ParseConversation([]byte(`{"id":4,"owner":{"conversationId":2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := odd.OwnerConversation(); !ok {
		t.Fatal("owner conversation present")
	}
	if _, ok := odd.OwnerTask(); ok {
		t.Fatal("owner task absent")
	}
}

func TestSubmissionColumns(t *testing.T) {
	s, err := ParseSubmission([]byte(`{"id":4,"conversationId":1,"requestId":"a\"b\u2028","type":"input","status":"placed","entry":9}`))
	if err != nil {
		t.Fatal(err)
	}
	col, ok := s.RequestColumn()
	if !ok || col != "\"a\\\"b\u2028\"" {
		t.Fatalf("request column %q %v (indexed text is JSON.stringify of the id)", col, ok)
	}
	if s.Status() != Placed {
		t.Fatal(s.Status())
	}
	if e, ok := s.Entry(); !ok || e != 9 {
		t.Fatal("entry")
	}
	none, _ := ParseSubmission([]byte(`{"id":5,"conversationId":1,"type":"write","status":"queued"}`))
	if _, ok := none.RequestColumn(); ok {
		t.Fatal("no request id binds NULL")
	}
}

func TestDocumentRecords(t *testing.T) {
	key := "k"
	d := NewDocumentCreate(12, "pi.live", &key, ScopeConversation, 3, "rewindable", "asOf")
	if string(d.Append(nil)) != `{"id":12,"kind":"pi.live","key":"k","scope":{"kind":"conversation","conversationId":3},"history":"rewindable","fork":"asOf"}` {
		t.Fatalf("%s", d.Append(nil))
	}
	stamped := d.Stamped(40, false)
	if !strings.HasSuffix(string(stamped.Append(nil)), `"fork":"asOf","createdAt":40}`) {
		t.Fatalf("%s", stamped.Append(nil))
	}
	both := d.Stamped(40, true)
	if !strings.HasSuffix(string(both.Append(nil)), `"createdAt":40,"retiredAt":40}`) {
		t.Fatalf("%s", both.Append(nil))
	}
	if bytes.Equal(d.Append(nil), stamped.Append(nil)) {
		t.Fatal("Stamped changed its receiver")
	}
	a := stamped.Address()
	if a.Kind != `"pi.live"` || a.KeyValue != `"k"` || a.Family != 1 || a.ScopeKind != "conversation" || a.OwnerID != 3 {
		t.Fatalf("address %+v", a)
	}
	single := NewDocumentCreate(13, "pi.usage", nil, ScopeSession, 0, "", "")
	sa := single.Address()
	if sa.Family != 0 || sa.KeyValue != `""` || sa.OwnerID != 0 {
		t.Fatalf("singleton address %+v", sa)
	}
	if !single.CurrentOnly() || d.CurrentOnly() || !NewDocumentCreate(1, "x", nil, ScopeConversation, 1, "latest", "initial").CurrentOnly() {
		t.Fatal("CurrentOnly")
	}
	if a.AddressKey() == sa.AddressKey() {
		t.Fatal("distinct addresses share a key")
	}
}

func TestDocumentAliveAt(t *testing.T) {
	d := NewDocumentCreate(1, "k", nil, ScopeSession, 0, "", "").Stamped(10, false)
	r := d.Retired(20)
	cases := []struct {
		doc     *Document
		current bool
		at      int64
		want    bool
	}{
		{d, true, 0, true}, {r, true, 0, false}, {d, false, 9, false}, {d, false, 10, true}, {r, false, 19, true}, {r, false, 20, false},
	}
	for i, c := range cases {
		if got := c.doc.AliveAt(c.current, c.at); got != c.want {
			t.Errorf("case %d: AliveAt(%v, %d) = %v", i, c.current, c.at, got)
		}
	}
}

func TestIndexedStringEscapesLikeJSON(t *testing.T) {
	for in, want := range map[string]string{
		"plain":            `"plain"`,
		"q\"\\":            `"q\"\\"`,
		"\n\t\x01":         `"\n\t\u0001"`,
		"\xed\xa0\x80lone": `"\ud800lone"`,
		"\u2028é":          "\"\u2028é\"",
	} {
		if got := IndexedString(in); got != want {
			t.Errorf("IndexedString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestStatusNames(t *testing.T) {
	for s := Pending; s <= Terminal; s++ {
		back, ok := ParseTaskStatus(s.String())
		if !ok || back != s {
			t.Errorf("task status %d", s)
		}
	}
	for s := Queued; s <= Unanswered; s++ {
		back, ok := ParseSubmissionStatus(s.String())
		if !ok || back != s {
			t.Errorf("submission status %d", s)
		}
	}
	if _, ok := ParseTaskStatus("done"); ok {
		t.Error("done is not a task status")
	}
}

// A record that is already a value reads exactly as its text does: the same checks, the same errors, and the same JSON back.
func TestOfReadsAValueAsParseReadsItsText(t *testing.T) {
	task := func(mut func(*jv.Object)) *jv.Object {
		o := NewTask(7, 3, "k", 1, jv.NewObject(), 0, false, "cp").V.Shallow()
		mut(o)
		return o
	}
	cases := map[string]*jv.Object{
		"task":          NewTask(7, 3, "k", 1, jv.NewObject(), 0, false, "cp").V,
		"negative zero": task(func(o *jv.Object) { o.Set("input", math.Copysign(0, -1)) }),
		"huge":          task(func(o *jv.Object) { o.Set("input", 1e21) }),
		"index keys": task(func(o *jv.Object) {
			in := jv.NewObject()
			in.Set("b", 1.0)
			in.Set("2", 2.0)
			in.Set("1", 3.0)
			o.Set("input", in)
		}),
		"lone surrogate": task(func(o *jv.Object) { o.Set("input", "a\xed\xa0\x80b") }),
		"no kind":        task(func(o *jv.Object) { o.Delete("kind") }),
		"bad id":         task(func(o *jv.Object) { o.Set("id", 1.5) }),
	}
	for name, o := range cases {
		viaText, errText := ParseTask(jv.Append(nil, o))
		viaValue, errValue := TaskOf(o)
		if (errText == nil) != (errValue == nil) || (errText != nil && errText.Error() != errValue.Error()) {
			t.Fatalf("%s: text %v, value %v", name, errText, errValue)
		}
		if errText == nil && !bytes.Equal(viaText.Append(nil), viaValue.Append(nil)) {
			t.Fatalf("%s: the value reads back as %s, its text as %s", name, viaValue.Append(nil), viaText.Append(nil))
		}
	}
	if _, err := TaskOf(nil); err == nil {
		t.Fatal("a nil record is a task")
	}
	if _, err := SubmissionOf(nil); err == nil {
		t.Fatal("a nil record is a submission")
	}
	if _, err := ConversationOf(nil); err == nil {
		t.Fatal("a nil record is a conversation")
	}
	if _, err := DocumentOf(nil); err == nil {
		t.Fatal("a nil record is a document")
	}
}
