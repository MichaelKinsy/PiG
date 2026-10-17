// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
)

// rangeDB is a store with the two tables a range derivation reads.
type rangeDB struct {
	t    *testing.T
	h    *Host
	next int64
}

func newRangeDB(t *testing.T) *rangeDB {
	t.Helper()
	db, err := OpenDB(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE conversations (id INTEGER PRIMARY KEY, record TEXT NOT NULL)`,
		`CREATE TABLE entries (id INTEGER PRIMARY KEY, conversation_id INTEGER NOT NULL, head INTEGER, commit_seq INTEGER NOT NULL, record TEXT NOT NULL)`,
		`CREATE INDEX entries_by_conversation ON entries (conversation_id, id)`,
	} {
		if _, err := db.Run(t.Context(), stmt, nil); err != nil {
			t.Fatal(err)
		}
	}
	return &rangeDB{t: t, h: &Host{opts: Options{DB: db}, ctx: t.Context()}, next: 1}
}

func (r *rangeDB) run(sql string, params ...abi.Value) {
	r.t.Helper()
	if _, err := r.h.opts.DB.Run(r.t.Context(), sql, params); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rangeDB) conversation(id int64, parent string) {
	rec := `{"id":` + strconv.FormatInt(id, 10) + parent + `}`
	r.run(`INSERT INTO conversations (id, record) VALUES (?, ?)`, abi.Int(id), abi.Text(rec))
}

// entry stores one entry of conv; model is the JSON of its messages, extra the rest of the record.
func (r *rangeDB) entry(conv int64, kind, model, extra string, head int64) int64 {
	id := r.next
	r.next++
	rec := `{"kind":"` + kind + `"`
	if model != "" {
		rec += `,"model":[` + model + `]`
	}
	rec += `,"id":` + strconv.FormatInt(id, 10) + `,"conversationId":` + strconv.FormatInt(conv, 10) + extra + `}`
	headVal := abi.Null()
	if head != 0 {
		headVal = abi.Int(head)
	}
	r.run(`INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)`, abi.Int(id), abi.Int(conv), headVal, abi.Int(id), abi.Text(rec))
	// The host's range store is told of every commit it applies (Host.observeRanges).
	if r.h.ranges == nil {
		r.h.ranges = history.NewStore()
	}
	if err := r.h.ranges.Append(conv, id, id, []byte(rec)); err != nil {
		r.t.Fatal(err)
	}
	return id
}

func (r *rangeDB) turn(conv int64, n int) (first int64) {
	s := strconv.Itoa(n)
	first = r.entry(conv, "pi.user", `{"role":"user","content":"question `+s+`","timestamp":`+s+`}`, "", 0)
	r.entry(conv, "pi.assistant", `{"role":"assistant","content":[{"type":"toolCall","id":"call_`+s+`","name":"read","arguments":{}}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2},"stopReason":"toolUse","timestamp":`+s+`}`, "", 0)
	r.entry(conv, "pi.tool-result", `{"role":"toolResult","toolCallId":"call_`+s+`","toolName":"read","content":[{"type":"text","text":"result `+s+`"}],"isError":false,"timestamp":`+s+`}`, "", 0)
	r.entry(conv, "pi.assistant", `{"role":"assistant","content":[{"type":"text","text":"answer `+s+`"}],"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2},"stopReason":"stop","timestamp":`+s+`}`, "", 0)
	return first
}

// rangeMessages is rangeContext's messages as comma-separated JSON.
func (h *Host) rangeMessages(conv, head, tail uint32) ([]byte, error) {
	r, err := h.rangeContext(conv, head, tail)
	if err != nil {
		return nil, err
	}
	return bytes.Join(r.Msgs, []byte(",")), nil
}

// fresh derives the same range with a host that holds nothing.
func (r *rangeDB) fresh(conv, head, tail uint32) []byte {
	r.t.Helper()
	other := &Host{opts: r.h.opts, ctx: r.h.ctx}
	got, err := other.rangeMessages(conv, head, tail)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

// TestRangeMessagesMatchAFreshDerivationAsTheTranscriptGrows checks the host's range derivation (a model_context range
// segment): a request after more commits extends what the host holds, reads only the new entries, and gives the bytes a
// host with nothing loaded derives; the head marker, a dangling tool call and a fork are derived by the same rules.
func TestRangeMessagesMatchAFreshDerivationAsTheTranscriptGrows(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(2, "")
	var keep int64
	for i := range 6 {
		first := r.turn(2, i)
		if i == 4 {
			keep = first
		}
	}
	tail := r.next - 1
	got, err := r.h.rangeMessages(2, 0, uint32(tail))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), `"role":"user"`); n != 6 {
		t.Fatalf("%d user messages in %s", n, got)
	}
	r.h.counters = Counters{}
	for i := 6; i < 9; i++ {
		r.turn(2, i)
		tail = r.next - 1
		got, err = r.h.rangeMessages(2, 0, uint32(tail))
		if err != nil {
			t.Fatal(err)
		}
		if want := r.fresh(2, 0, uint32(tail)); !bytes.Equal(got, want) {
			t.Fatalf("turn %d: extended derivation differs from a fresh one:\n got %s\nwant %s", i, got, want)
		}
	}
	if rows := r.h.counters.RowsRead; rows > 3*(4+1+2) {
		t.Fatalf("%d rows read while extending by 3 turns of 4 entries, want only the new entries and the bounds", rows)
	}

	// A head marker: the context starts at the marker's head, with its summary first.
	marker := r.entry(2, "pi.compaction", `{"role":"user","content":[{"type":"text","text":"summary"}],"timestamp":1}`, `,"head":`+strconv.FormatInt(keep, 10), keep)
	tail = r.next - 1
	got, err = r.h.rangeMessages(2, uint32(marker), uint32(tail))
	if err != nil {
		t.Fatal(err)
	}
	if want := r.fresh(2, uint32(marker), uint32(tail)); !bytes.Equal(got, want) {
		t.Fatalf("head marker: got %s want %s", got, want)
	}
	if !strings.HasPrefix(string(got), `{"role":"user","content":[{"type":"text","text":"summary"}]`) || strings.Contains(string(got), `"question 0"`) {
		t.Fatalf("a range under a head marker starts at the marker: %s", got)
	}

	// A call whose result is missing gets the synthesized result of spec section 2.1.
	r.entry(2, "pi.user", `{"role":"user","content":"again","timestamp":50}`, "", 0)
	r.entry(2, "pi.assistant", `{"role":"assistant","content":[{"type":"toolCall","id":"orphan","name":"read","arguments":{}}],"stopReason":"toolUse","timestamp":51}`, "", 0)
	tail = r.next - 1
	got, err = r.h.rangeMessages(2, uint32(marker), uint32(tail))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), `"details":{"reason":"missing_result"},"timestamp":51}`) && !strings.Contains(string(got), `"toolCallId":"orphan"`) {
		t.Fatalf("the dangling call has no synthesized result: %s", got)
	}

	// A fork sees its parent's entries up to the fork point, then its own.
	r.conversation(3, `,"parent":{"conversationId":2,"at":`+strconv.FormatInt(keep+3, 10)+`}`)
	r.entry(3, "pi.user", `{"role":"user","content":"fork","timestamp":60}`, "", 0)
	forkTail := r.next - 1
	got, err = r.h.rangeMessages(3, 0, uint32(forkTail))
	if err != nil {
		t.Fatal(err)
	}
	if want := r.fresh(3, 0, uint32(forkTail)); !bytes.Equal(got, want) {
		t.Fatalf("fork: got %s want %s", got, want)
	}
	if !strings.Contains(string(got), `"question 4"`) || strings.Contains(string(got), `"question 5"`) || !strings.Contains(string(got), `"fork"`) {
		t.Fatalf("fork context: %s", got)
	}
}

// TestRangeMessagesRejectABoundsMismatch checks that a range naming a head marker or a tail the store disagrees with is an
// error, not a different context.
func TestRangeMessagesRejectABoundsMismatch(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(2, "")
	r.turn(2, 0)
	tail := r.next - 1
	for name, c := range map[string][3]uint32{
		"a head marker the context lacks": {2, 3, uint32(tail)},
		"an unknown conversation":         {9, 0, uint32(tail)},
	} {
		if _, err := r.h.rangeMessages(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := r.h.rangeMessages(2, 0, uint32(tail+5)); err == nil || !strings.Contains(err.Error(), "is not visible from conversation 2") {
		t.Errorf("a tail beyond the conversation: error %v, want Pi's not-visible error", err)
	}
}

// The range carries the identity of the stored bytes each message came from: the messages of an earlier request keep their
// identities as the transcript grows (a handler decodes only the new ones), each is exactly one message, and a released
// conversation derives new identities.
func TestRangeContextIdentitiesAreStableAsTheTranscriptGrows(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(2, "")
	// The host learns of a conversation it sees created: its chain starts complete and a commit extends it.
	r.h.ranges = history.NewStore()
	r.h.ranges.AddConversation(history.ConvRecord{ID: 2}, true)
	for i := range 4 {
		r.turn(2, i)
	}
	first, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	r.turn(2, 4)
	second, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Idents) <= len(first.Idents) {
		t.Fatalf("%d messages after growth, %d before", len(second.Idents), len(first.Idents))
	}
	for i := range first.Idents {
		if !first.Stored[i] || first.Idents[i] != second.Idents[i] {
			t.Fatalf("message %d changed identity as the transcript grew", i)
		}
	}
	for i, msg := range second.Msgs {
		if len(msg) < 2 || msg[0] != '{' || msg[len(msg)-1] != '}' {
			t.Fatalf("message %d is %q", i, msg)
		}
	}
	r.h.ranges.Release(2)
	third, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	if third.Idents[0] == first.Idents[0] {
		t.Fatal("a message of a released conversation kept its identity")
	}
}

// A context loaded from storage through the newest entry stays loaded as commits arrive: the range of the next request keeps
// the identities of the messages before the new ones (the conversation was not created by this host, so its chain is loaded,
// not started complete), and an earlier cutoff still answers for that entry.
func TestRangeContextOfAStoredConversationSurvivesLaterCommits(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(2, "")
	for i := range 4 {
		r.turn(2, i)
	}
	first, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	mid := r.next - 1
	r.turn(2, 4)
	second, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Idents {
		if first.Idents[i] != second.Idents[i] {
			t.Fatalf("message %d changed identity when a turn was committed after the context was loaded", i)
		}
	}
	earlier, err := r.h.rangeContext(2, 0, uint32(mid))
	if err != nil || len(earlier.Idents) != len(first.Idents) {
		t.Fatalf("the context through entry %d: %v, %d messages, want %d", mid, err, len(earlier.Idents), len(first.Idents))
	}
}

// A range shares the store's record text instead of copying it, and a handler reads it while the host goes on: later commits,
// a trim at a head marker and a release of the conversation leave the bytes a range handed out as they were.
func TestRangeMessagesStayIntactWhileTheHostGoesOn(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(2, "")
	r.h.ranges = history.NewStore()
	r.h.ranges.SetTrimAtHead()
	r.h.ranges.AddConversation(history.ConvRecord{ID: 2}, true)
	var keep int64
	for i := range 6 {
		first := r.turn(2, i)
		if i == 4 {
			keep = first
		}
	}
	rng, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(rng.Msgs))
	for i, m := range rng.Msgs {
		want[i] = string(m)
	}
	for i := 6; i < 12; i++ {
		r.turn(2, i)
	}
	marker := r.entry(2, "pi.compaction", `{"role":"user","content":[{"type":"text","text":"summary"}],"timestamp":1}`, `,"head":`+strconv.FormatInt(keep, 10), keep)
	if _, err := r.h.rangeContext(2, uint32(marker), uint32(r.next-1)); err != nil {
		t.Fatal(err)
	}
	r.h.ranges.Release(2)
	r.turn(2, 12)
	if _, err := r.h.rangeContext(2, uint32(marker), uint32(r.next-1)); err != nil {
		t.Fatal(err)
	}
	for i, m := range rng.Msgs {
		if string(m) != want[i] {
			t.Fatalf("message %d changed after the host went on:\n got %s\nwant %s", i, m, want[i])
		}
	}
}
