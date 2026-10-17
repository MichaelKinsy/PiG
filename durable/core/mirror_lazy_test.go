// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// The mirror holds the entries this handle wrote only while they are few: once they pass heldKeep and no line job's
// statements wait for a COMMIT hop, the host applied every commit of the earlier steps (ABI section 3 rule 2), so the
// next Step releases them and reads them from storage. Without it every entry a long-lived handle wrote stayed in Wasm
// memory. A cached head-marker answer moves with them, so the release reads nothing.
func TestStepStartReleasesEntriesStorageHas(t *testing.T) {
	big := []byte(`{"id":8,"conversationId":1,"head":7,"text":"` + strings.Repeat("x", heldKeep) + `"}`)
	var reads []uint16
	h := &handler{mir: newMirror()}
	m := h.mir
	m.lazy = lazyState{
		read: func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
			reads = append(reads, sql)
			switch {
			case sql == store.SQLEntryByID && params[0].Number() == 8:
				return [][]abi.Value{{abi.Int(1), abi.Int(5), {Kind: abi.ValueBlob, Bytes: big}}}, nil
			case sql == store.SQLEntryBounds:
				return [][]abi.Value{{abi.Int(6), abi.Int(3), abi.Int(1)}}, nil // newest stored entry 6, head marker 3 (head 1)
			}
			return nil, nil
		},
		below: 7,
	}
	marker := func() (session.ID, int64) {
		b, ok := m.entryBounds(1, 0)
		if !ok || !b.hasMarker {
			t.Fatalf("no head marker: %+v", b)
		}
		return b.markerID, b.markerHead
	}
	if id, head := marker(); id != 3 || head != 1 || len(reads) != 1 {
		t.Fatalf("stored head marker %d (head %d) after %d reads", id, head, len(reads))
	}
	// A turn is in progress (task 2 is live): the mirror keeps what it wrote until heldKeep.
	m.putTask(2, taskHead{conv: 1, kind: "k", status: "running"}, parseRaw([]byte(`{"id":2,"conversationId":1,"kind":"k","state":{"status":"running"}}`)), nil)
	m.entries.put(7, 1, 4, false, []byte(`{"id":7,"conversationId":1}`))
	m.nextID = 8
	h.StepStart()
	if !m.entries.has(7) {
		t.Fatal("an entry was released while a task is live and the held entries are under heldKeep")
	}
	m.entries.put(8, 1, 5, true, big)
	m.nextID = 9
	h.held = []abi.Commit{{Seq: 5}}
	h.StepStart()
	if !m.entries.has(8) || m.storedBelow() != 7 {
		t.Fatal("an entry whose commit still waits for its COMMIT hop was released")
	}
	h.held = nil
	h.StepStart()
	if m.entries.has(7) || m.entries.has(8) || m.storedBelow() != 9 {
		t.Fatalf("the applied entries are still held (stored below %d)", m.storedBelow())
	}
	if id, head := marker(); id != 8 || head != 7 || len(reads) != 1 {
		t.Fatalf("head marker after the release %d (head %d) after %d reads", id, head, len(reads))
	}
	raw, seq, ok := m.entryRaw(8)
	if !ok || len(raw) != len(big) || seq != 5 || len(reads) != 2 || reads[1] != store.SQLEntryByID {
		t.Fatalf("entry 8 after release: %d bytes seq %d ok %v reads %v", len(raw), seq, ok, reads)
	}
	// The turn ends (task 2 is terminal): the next Step start releases what the mirror holds however little it is, so
	// a handle holds the same live set after every turn.
	m.entries.put(9, 1, 6, false, []byte(`{"id":9,"conversationId":1}`))
	m.putTask(2, taskHead{conv: 1, kind: "k", status: "terminal"}, nil, []byte(`{"id":2}`))
	m.nextID = 10
	if m.heldBytes() == 0 {
		t.Fatal("the mirror holds the turn's records before the Step start")
	}
	h.StepStart()
	if m.entries.has(9) || m.tasks.has(2) || m.heldBytes() != 0 || m.storedBelow() != 10 {
		t.Fatalf("a quiet Step start kept %d bytes (entry 9 %v, task 2 %v, stored below %d)", m.heldBytes(), m.entries.has(9), m.tasks.has(2), m.storedBelow())
	}
}

// The stored-entry bound splits entries into stored ones (below it) and held ones; a new entry below it would be in
// neither half, so the commit is refused before the store emits it.
func TestAnEntryBelowTheStoredBoundIsRefused(t *testing.T) {
	m := newMirror()
	m.lazy = lazyState{read: func(uint16, []abi.Value) ([][]abi.Value, error) { return nil, nil }, below: 9}
	rec := jv.NewObject()
	rec.Set("id", float64(8))
	if err := m.checkGlobalIDs([]session.Write{{Kind: session.WriteEntry, Record: rec}}); err == nil {
		t.Fatal("entry 8 below the stored-entry bound 9 was accepted")
	}
	rec.Set("id", float64(9))
	if err := m.checkGlobalIDs([]session.Write{{Kind: session.WriteEntry, Record: rec}}); err != nil {
		t.Fatal(err)
	}
}

// Terminal tasks and settled submissions this handle wrote are released the same way once heldSettled of them are
// held: a read of one then goes to storage, a request ID resolves from storage, and the live ones stay. Pi's Session
// keeps none of them, and a handle that keeps them all grows every turn (robust-durable F1).
func TestStepStartReleasesSettledTasksAndSubmissions(t *testing.T) {
	const task10 = `{"id":10,"conversationId":1,"kind":"k","state":{"status":"terminal"}}`
	var reads []uint16
	h := &handler{mir: newMirror()}
	m := h.mir
	m.lazy = lazyState{
		read: func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
			reads = append(reads, sql)
			switch {
			case sql == store.SQLTaskByID && params[0].Number() == 10:
				return [][]abi.Value{{abi.TextBytes([]byte(task10))}}, nil
			case sql == store.SQLSubmissionByRequest:
				return [][]abi.Value{{abi.TextBytes([]byte(`{"id":11,"conversationId":1,"requestId":"r","status":"done"}`))}}, nil
			}
			return nil, nil
		},
		below: 10,
	}
	if m.task(5) != nil || len(reads) != 1 {
		t.Fatal("task 5 is absent from storage")
	}
	m.putTask(10, taskHead{conv: 1, kind: "k", status: "terminal"}, parseRaw([]byte(task10)), nil)
	sub := parseRaw([]byte(`{"id":11,"conversationId":1,"requestId":"r","status":"done"}`))
	m.addSubmission(11, sub, nil)
	m.putTask(12, taskHead{conv: 1, kind: "k", status: "running"}, parseRaw([]byte(`{"id":12,"conversationId":1,"kind":"k","state":{"status":"running"}}`)), nil)
	m.nextID = 13
	h.StepStart()
	if !m.tasks.has(10) || !m.subs.has(11) {
		t.Fatal("settled records were released under heldSettled")
	}
	for id := session.ID(13); id < 13+heldSettled; id++ {
		m.putTask(id, taskHead{conv: 1, status: "terminal"}, nil, []byte(`{}`))
	}
	m.nextID = 13 + heldSettled
	h.StepStart()
	if m.tasks.has(10) || m.subs.has(11) || !m.tasks.has(12) || m.storedBelow() != 13+heldSettled {
		t.Fatalf("after the release: task 10 %v, submission 11 %v, live task 12 %v, below %d", m.tasks.has(10), m.subs.has(11), m.tasks.has(12), m.storedBelow())
	}
	reads = nil
	if task := m.task(10); task == nil || num(task, "id") != 10 || len(reads) != 1 || reads[0] != store.SQLTaskByID {
		t.Fatalf("released task 10: %v, reads %v", task, reads)
	}
	if got, err := m.SubmissionByRequest(1, "r"); err != nil || got == nil || num(got, "id") != 11 {
		t.Fatalf("released submission by request: %v %v", got, err)
	}
	if m.task(5) != nil || reads[len(reads)-1] != store.SQLTaskByID {
		t.Fatalf("an absent answer from before the release still stands: reads %v", reads)
	}
}

// The terminal and settled counts releaseApplied reads every step follow each record through its writes and the release:
// counting the heads at each step made a step cost O(held tasks), and a 20,000-deep chain of ended tasks never finished
// on the TinyGo core within a minute (U-harness-scheduler-retention).
func TestTheHeldCountsFollowEveryWrite(t *testing.T) {
	m := newMirror()
	check := func(when string, live bool, settled int) {
		t.Helper()
		n, anyLive := 0, false
		for _, h := range m.tasks.heads {
			if h.status == "terminal" {
				n++
			} else {
				anyLive = true
			}
		}
		for _, h := range m.subs.heads {
			if settledStatus(h.status) {
				n++
			}
		}
		if m.liveHeld() != live || m.settledHeld() != settled || n != settled || anyLive != live {
			t.Fatalf("%s: liveHeld %v settledHeld %d, the heads count %v %d, want %v %d", when, m.liveHeld(), m.settledHeld(), anyLive, n, live, settled)
		}
	}
	check("empty", false, 0)
	m.putTask(2, taskHead{conv: 1, status: "running"}, jv.NewObject(), nil)
	m.putTask(3, taskHead{conv: 1, status: "pending"}, jv.NewObject(), nil)
	check("two live tasks", true, 0)
	m.putTask(2, taskHead{conv: 1, status: "terminal"}, nil, []byte(`{}`))
	m.putTask(2, taskHead{conv: 1, status: "terminal"}, nil, []byte(`{}`))
	check("task 2 ended, written twice", true, 1)
	m.putSubmission(4, subHead{conv: 1, status: "pending"}, jv.NewObject(), nil)
	m.putSubmission(4, subHead{conv: 1, status: "done"}, nil, []byte(`{}`))
	check("submission 4 settled", true, 2)
	m.putTask(3, taskHead{conv: 1, status: "terminal"}, nil, []byte(`{}`))
	check("every task ended", false, 3)
	m.tasks.keepLive()
	m.subs.keepLive()
	check("after the release", false, 0)
	m.putTask(5, taskHead{conv: 1, status: "terminal"}, nil, []byte(`{}`))
	check("an ended task after the release", false, 1)
}

// TestAPageOfStoredEntriesIsOneRead is the newest-first Conversation.entries page of a long stored transcript: its records come
// with the ids a page at a time, not one read an entry. A 500-entry page over 13,000 stored entries was 24-78 times Pi's
// time while it read each record by id.
func TestAPageOfStoredEntriesIsOneRead(t *testing.T) {
	const stored = 13000
	var reads []uint16
	m := newMirror()
	m.convs.put(1, jv.NewObject())
	m.lazy = lazyState{
		read: func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
			reads = append(reads, sql)
			record := func(id int64) []abi.Value {
				rec := []byte(`{"id":` + itoa(id) + `,"conversationId":1,"kind":"pi.user"}`)
				return []abi.Value{abi.Int(id), abi.Int(id), {Kind: abi.ValueBlob, Bytes: rec}}
			}
			var rows [][]abi.Value
			switch sql {
			case store.SQLEntriesDesc:
				lo, hi, n := params[1].Int, params[2].Int, params[3].Int
				for id := hi; id >= lo && int64(len(rows)) < n; id-- {
					rows = append(rows, record(id))
				}
			case store.SQLEntriesPage:
				after, hi := params[1].Int, params[2].Int
				for id := after + 1; id <= hi && len(rows) < store.PageRows; id++ {
					rows = append(rows, record(id))
				}
			default:
				t.Errorf("statement %d: a page of stored entries reads only whole pages of records", sql)
			}
			return rows, nil
		},
		below: stored + 1,
	}
	page, err := m.ScanEntries(session.EntryQuery{ConversationID: 1}, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 500 || num(page.Items[0], "id") != stored || num(page.Items[499], "id") != stored-499 || page.After != stored-499 {
		t.Fatalf("page of %d, first %v, last %v, after %d", len(page.Items), page.Items[0], page.Items[len(page.Items)-1], page.After)
	}
	if len(reads) != 1 {
		t.Fatalf("%d reads for a page of 500, want 1", len(reads))
	}
	next, err := m.ScanEntries(session.EntryQuery{ConversationID: 1}, 500, page.After)
	if err != nil || len(next.Items) != 500 || num(next.Items[0], "id") != stored-500 || len(reads) != 2 {
		t.Fatalf("the next page: %d items, %v, %d reads, %v", len(next.Items), next.Items[0], len(reads), err)
	}
	// An oldest-first page reads a page of records from its cursor, not every id of the conversation.
	reads = nil
	up, err := m.ScanEntries(session.EntryQuery{ConversationID: 1, Ascending: true}, 500, 0)
	if err != nil || len(up.Items) != 500 || num(up.Items[0], "id") != 1 || num(up.Items[499], "id") != 500 || up.After != 500 || len(reads) != 1 {
		t.Fatalf("the first ascending page: %d items, after %d, %d reads, %v", len(up.Items), up.After, len(reads), err)
	}
	up, err = m.ScanEntries(session.EntryQuery{ConversationID: 1, Ascending: true}, 500, up.After)
	if err != nil || len(up.Items) != 500 || num(up.Items[0], "id") != 501 || len(reads) != 2 {
		t.Fatalf("the second ascending page: %d items, %d reads, %v", len(up.Items), len(reads), err)
	}
	last, err := m.ScanEntries(session.EntryQuery{ConversationID: 1, Ascending: true}, 500, stored-10)
	if err != nil || len(last.Items) != 10 || last.After != 0 || num(last.Items[9], "id") != stored {
		t.Fatalf("the last ascending page: %d items, after %d, %v", len(last.Items), last.After, err)
	}
}

// A newest-first scan with a limit reads no more stored entries than it walks (the page and one more): the first
// page of a released conversation is limit+1 rows, not a fixed page, so the turn-end release costs a turn a few rows.
func TestADescendingScanReadsOnlyThePageItNeeds(t *testing.T) {
	m := newMirror()
	m.convs.put(1, parseRaw([]byte(`{"id":1}`)))
	var limits []int64
	m.lazy = lazyState{
		read: func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
			if sql != store.SQLEntriesDesc {
				return nil, nil
			}
			limits = append(limits, int64(params[3].Number()))
			var rows [][]abi.Value
			for id := int64(params[2].Number()); id >= int64(params[1].Number()) && len(rows) < int(params[3].Number()); id-- {
				rows = append(rows, []abi.Value{abi.Int(id), abi.Int(id), abi.TextBytes([]byte(`{"id":` + strconv.FormatInt(id, 10) + `,"conversationId":1}`))})
			}
			return rows, nil
		},
		below: 100,
	}
	page, err := m.ScanEntries(session.EntryQuery{ConversationID: 1}, 1, 0)
	if err != nil || len(page.Items) != 1 || num(page.Items[0], "id") != 99 {
		t.Fatalf("newest entry: %+v %v", page, err)
	}
	if len(limits) != 1 || limits[0] != 2 {
		t.Fatalf("a scan for one entry read stored record pages of %v rows, want one page of 2", limits)
	}
}

// The api's entries page splices each stored record's text instead of parsing the record and writing the tree out again; the
// answer is the same bytes, as stored records are what JSON.stringify wrote (characters raw, lone surrogates and controls escaped).
func TestARawEntriesPageIsTheBytesOfTheParsedPage(t *testing.T) {
	texts := []string{
		`{"kind":"pi.user","model":[{"role":"user","content":"café \"quoted\" \\ 😀 \ud800 line\nbreak\u0001","timestamp":1}],"id":%d,"conversationId":1}`,
		`{"kind":"pi.assistant","model":[{"role":"assistant","content":[{"type":"toolCall","id":"c%d","name":"lookup","arguments":{"n":%d,"x":0.1,"big":1e+21}}],"usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15},"stopReason":"toolUse","timestamp":2}],"id":%d,"conversationId":1,"durationMs":0}`,
	}
	m := newMirror()
	m.convs.put(1, jv.NewObject())
	m.lazy = lazyState{
		read: func(sql uint16, params []abi.Value) ([][]abi.Value, error) {
			var rows [][]abi.Value
			for id := params[1].Int + 1; id <= params[2].Int && len(rows) < store.PageRows; id++ {
				rec := []byte(strings.NewReplacer("%d", strconv.FormatInt(id, 10)).Replace(texts[id%2]))
				rows = append(rows, []abi.Value{abi.Int(id), abi.Int(id), {Kind: abi.ValueBlob, Bytes: rec}})
			}
			return rows, nil
		},
		below: 41,
	}
	for _, ascending := range []bool{true, false} {
		q := session.EntryQuery{ConversationID: 1, Ascending: ascending}
		parsed, err := m.ScanEntries(q, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		items, next, err := m.scanEntriesRaw(q, 20, 0)
		if err != nil {
			t.Fatal(err)
		}
		if next != parsed.After || len(items) != len(parsed.Items) || len(items) != 20 {
			t.Fatalf("ascending=%v: %d raw items after %d, %d parsed after %d", ascending, len(items), next, len(parsed.Items), parsed.After)
		}
		order := "descending"
		if ascending {
			order = "ascending"
		}
		want, got := jv.Append(nil, page(parsed, order)), jv.Append(nil, rawPage(items, next, order))
		if !bytes.Equal(got, want) {
			at := 0
			for at < len(got) && at < len(want) && got[at] == want[at] {
				at++
			}
			lo := max(at-60, 0)
			t.Fatalf("ascending=%v: the raw page differs from the parsed page at byte %d:\n got ...%s\nwant ...%s", ascending, at, got[lo:min(at+60, len(got))], want[lo:min(at+60, len(want))])
		}
	}
}
