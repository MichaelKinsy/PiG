// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// transcript builds a conversation of tool-calling turns as stored records, the shape durable-bench writes: per
// turn a user entry, an assistant entry with one call, its result, and a final answer.
type transcript struct {
	t      testing.TB
	db     *fakeDB
	conv   int64
	nextID int64
	seq    int64
}

func newTranscript(t testing.TB, conv int64) *transcript {
	db := newFakeDB()
	db.convs[conv] = AppendConversation(nil, ConvRecord{ID: conv})
	return &transcript{t: t, db: db, conv: conv, nextID: conv + 1}
}

func (tr *transcript) add(kind, model string, extra string) int64 {
	id := tr.nextID
	tr.nextID++
	tr.seq++
	rec := `{"kind":"` + kind + `"`
	if model != "" {
		rec += `,"model":[` + model + `]`
	}
	rec += `,"id":` + strconv.FormatInt(id, 10) + `,"conversationId":` + strconv.FormatInt(tr.conv, 10) + extra + `}`
	tr.db.addEntry(tr.t, tr.conv, tr.seq, []byte(rec))
	return id
}

func (tr *transcript) turn(n int) (first int64) {
	s := strconv.Itoa(n)
	first = tr.add("pi.user", `{"role":"user","content":"question `+s+`","timestamp":`+s+`}`, "")
	tr.add("pi.assistant", `{"role":"assistant","content":[{"type":"toolCall","id":"call_`+s+`","name":"read","arguments":{"n":`+s+`}}],"usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15},"stopReason":"toolUse","timestamp":`+s+`}`, "")
	tr.add("pi.tool-result", `{"role":"toolResult","toolCallId":"call_`+s+`","toolName":"read","content":[{"type":"text","text":"result `+s+`"}],"isError":false,"timestamp":`+s+`}`, "")
	tr.add("pi.assistant", `{"role":"assistant","content":[{"type":"text","text":"answer `+s+`"}],"usage":{"input":20,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":25},"stopReason":"stop","timestamp":`+s+`}`, "")
	return first
}

func (tr *transcript) compact(firstKept int64) int64 {
	return tr.add("pi.compaction", `{"role":"user","content":[{"type":"text","text":"summary"}],"timestamp":1}`, `,"head":`+strconv.FormatInt(firstKept, 10))
}

// TestColdOpenReadsOnlyTheActiveRange is ADR-0001 D5 / H-D5: with a head marker, the rows a cold open reads do not
// grow with history; without one they are the whole transcript.
func TestColdOpenReadsOnlyTheActiveRange(t *testing.T) {
	for _, turns := range []int{50, 400, 1600} {
		tr := newTranscript(t, 2)
		var keep int64
		for i := range turns {
			first := tr.turn(i)
			if i == turns-10 {
				keep = first
			}
		}
		tr.compact(keep)
		st := NewStore()
		v, err := tr.db.context(t, st, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := v.NumEntries(), 1+10*4; got != want {
			t.Fatalf("%d turns: %d active entries, want %d", turns, got, want)
		}
		// conversation row + bounds (3 columns, 2 rows) + 41 range rows + 1 marker.
		if tr.db.rowsRead > 60 {
			t.Fatalf("%d turns: cold open read %d rows, want a constant", turns, tr.db.rowsRead)
		}
		if tr.db.reads != 3 {
			t.Fatalf("%d turns: %d reads, want conversation, bounds, entries", turns, tr.db.reads)
		}
		if n := st.chainList[0].Len(); n != 41 {
			t.Fatalf("%d turns: %d entries loaded, want 41", turns, n)
		}
	}
	// Without a marker the active range is the whole history.
	tr := newTranscript(t, 2)
	for i := range 100 {
		tr.turn(i)
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	if st.chainList[0].Len() != 400 {
		t.Fatalf("loaded %d entries, want all 400", st.chainList[0].Len())
	}
}

// TestWarmTurnExtendsTheCachedContext checks that appending entries extends the derived context without scanning
// the range again, and that the extension equals a derivation from scratch at every step.
func TestWarmTurnExtendsTheCachedContext(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 30 {
		tr.turn(i)
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	if st.rebuilt != 1 {
		t.Fatalf("rebuilt = %d", st.rebuilt)
	}
	start := len(tr.db.entries)
	for i := 30; i < 36; i++ {
		tr.turn(i)
		for k, row := range tr.db.entries[start:] {
			if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
				t.Fatal(err)
			}
			v, err := tr.db.context(t, st, 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			upTo := &fakeDB{convs: tr.db.convs, entries: tr.db.entries[:start+k+1]}
			fresh, err := upTo.context(t, NewStore(), 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := formatView(v), formatView(fresh); got != want {
				t.Fatalf("after entry %d:\n got  %s\n want %s", row.id, got, want)
			}
		}
		start = len(tr.db.entries)
	}
	if st.rebuilt != 1 || st.extended != 24 {
		t.Fatalf("rebuilt %d, extended %d; want 1 and 24", st.rebuilt, st.extended)
	}
}

// TestCutoffAtTheNewestEntryExtendsTheKeptContext checks that a read cut off at the newest entry, as a generation's is,
// reuses and extends the kept context instead of deriving the range again, equals a derivation from scratch, and leaves a
// view an earlier read returned unchanged (a request waiting on hooks while another task reads the conversation).
func TestCutoffAtTheNewestEntryExtendsTheKeptContext(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 30 {
		tr.turn(i)
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	start := len(tr.db.entries)
	held, err := tr.db.context(t, st, 2, tr.db.entries[start-1].id)
	if err != nil {
		t.Fatal(err)
	}
	heldBefore := formatView(held)
	for i := 30; i < 36; i++ {
		tr.turn(i)
	}
	for k, row := range tr.db.entries[start:] {
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			t.Fatal(err)
		}
		v, err := tr.db.context(t, st, 2, row.id)
		if err != nil {
			t.Fatal(err)
		}
		upTo := &fakeDB{convs: tr.db.convs, entries: tr.db.entries[:start+k+1]}
		fresh, err := upTo.context(t, NewStore(), 2, row.id)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := formatView(v), formatView(fresh); got != want {
			t.Fatalf("after entry %d:\n got  %s\n want %s", row.id, got, want)
		}
	}
	if st.rebuilt != 1 || st.extended != len(tr.db.entries)-start {
		t.Fatalf("rebuilt %d, extended %d; want 1 and %d", st.rebuilt, st.extended, len(tr.db.entries)-start)
	}
	if got := formatView(held); got != heldBefore {
		t.Fatalf("a view of an earlier read changed when later reads extended the context:\n got  %s\n want %s", got, heldBefore)
	}
}

// TestSystemMessagesComeFromTheDerivation checks that a context's system messages are listed while it is derived, so a
// request does not scan every message (the text scan this replaced allocated per message under TinyGo): reading them
// allocates nothing, they equal the messages of that role in order, and a context extended by new entries keeps the list
// current without changing a view an earlier read returned.
func TestSystemMessagesComeFromTheDerivation(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 40 {
		tr.turn(i)
		if i%7 == 0 {
			tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"sections `+strconv.Itoa(i)+`"}],"timestamp":1}`, "")
		}
	}
	st := NewStore()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	checkMessageRoles(t, v)
	n := v.NumSystemMessages()
	if n != 6 {
		t.Fatalf("%d system messages, want 6", n)
	}
	allocs := testing.AllocsPerRun(20, func() {
		for j := 0; j < v.NumSystemMessages(); j++ {
			_ = v.SystemMessage(j)
		}
	})
	if allocs != 0 {
		t.Fatalf("reading the system messages allocated %v times", allocs)
	}
	before := string(v.SystemMessage(n - 1))
	start := len(tr.db.entries)
	tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"later"}],"timestamp":2}`, "")
	tr.turn(99)
	for _, row := range tr.db.entries[start:] {
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			t.Fatal(err)
		}
	}
	w, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	checkMessageRoles(t, w)
	if w.NumSystemMessages() != n+1 {
		t.Fatalf("after the extension %d system messages, want %d", w.NumSystemMessages(), n+1)
	}
	if v.NumSystemMessages() != n || string(v.SystemMessage(n-1)) != before {
		t.Fatal("extending the context changed the system messages of an earlier view")
	}
}

// TestSettledMessagesAreNotCopiedPerRequest checks that deriving the context again after one more turn allocates for the
// open part, not for every settled message (it copied the whole list per request), and that the extension equals a
// derivation from scratch at every step, including where a system message is led to the front: while only user messages
// are settled (the open part decides), when it arrives after a settled assistant message (no lead), and when it is the
// first non-user message of the settled part. A view an earlier read returned keeps its messages.
func TestSettledMessagesAreNotCopiedPerRequest(t *testing.T) {
	system := func(tr *transcript, s string) {
		tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"`+s+`"}],"timestamp":1}`, "")
	}
	for _, scenario := range []string{"users-then-system", "system-after-assistant", "system-first"} {
		tr := newTranscript(t, 2)
		switch scenario {
		case "users-then-system":
			tr.add("pi.user", `{"role":"user","content":"a","timestamp":1}`, "")
			tr.add("pi.user", `{"role":"user","content":"b","timestamp":2}`, "")
			system(tr, "late")
		case "system-after-assistant":
			tr.turn(0)
			system(tr, "later")
		case "system-first":
			system(tr, "first")
		}
		st := NewStore()
		first, err := tr.db.context(t, st, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		held, heldView := first, formatView(first)
		start := len(tr.db.entries)
		for i := 1; i < 6; i++ {
			tr.turn(i)
			if i == 3 {
				system(tr, "again")
			}
		}
		for k, row := range tr.db.entries[start:] {
			if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
				t.Fatal(err)
			}
			v, err := tr.db.context(t, st, 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := (&fakeDB{convs: tr.db.convs, entries: tr.db.entries[:start+k+1]}).context(t, NewStore(), 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := formatView(v), formatView(fresh); got != want {
				t.Fatalf("%s, after entry %d:\n got  %s\n want %s", scenario, row.id, got, want)
			}
		}
		if got := formatView(held); got != heldView {
			t.Fatalf("%s: a view of an earlier read changed:\n got  %s\n want %s", scenario, got, heldView)
		}
	}

	tr := newTranscript(t, 2)
	for i := range 3000 {
		tr.turn(i)
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	// One turn first: the first append to a loaded chain reallocates its index slices once, with room for the next ones
	// (grow.go), and that one reallocation is not a per-request copy.
	warm := len(tr.db.entries)
	tr.turn(3000)
	for _, row := range tr.db.entries[warm:] {
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.db.context(t, st, 2, 0); err != nil {
			t.Fatal(err)
		}
	}
	start := len(tr.db.entries)
	tr.turn(3001)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, row := range tr.db.entries[start:] {
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.db.context(t, st, 2, 0); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	// 12,000 settled messages at 8 bytes each is 96 KB per request; the open part of a turn is a few messages.
	if per := (after.TotalAlloc - before.TotalAlloc) / uint64(len(tr.db.entries)-start); per > 24<<10 {
		t.Fatalf("a request after one new entry allocated %d bytes: the settled messages are copied again", per)
	}
}

// TestCallIDHashIsTheHashOfTheDecodedString checks the hash that tells two tool call ids apart without reading them: ids
// compare as JavaScript strings, so an id written with an escape hashes like the same id written plainly.
func TestCallIDHashIsTheHashOfTheDecodedString(t *testing.T) {
	if hashID([]byte(`call_\u0061\n`)) != hashID([]byte("call_a\n")) {
		t.Fatal("an escaped id hashes differently from the string it decodes to")
	}
	if hashID([]byte("call_a")) == hashID([]byte("call_b")) {
		t.Fatal("different ids share a hash")
	}
}

// TestRecordsAreReadByLengthNotByTheNextOffset checks that a stored record is read with its own length (the host-held
// design has no neighbouring record to end it): every active entry's record equals the bytes appended, for records of
// different sizes, loaded cold and appended warm.
func TestRecordsAreReadByLengthNotByTheNextOffset(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 12 {
		tr.turn(i)
		if i%3 == 0 {
			tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"`+strings.Repeat("s", 50*i+1)+`"}],"timestamp":1}`, "")
		}
	}
	st := NewStore()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][]byte{}
	for _, row := range tr.db.entries {
		want[row.id] = row.rec
	}
	for i := 0; i < v.NumEntries(); i++ {
		if got := v.EntryRecord(i); !bytes.Equal(got, want[v.EntryID(i)]) {
			t.Fatalf("entry %d: record %q, appended %q", v.EntryID(i), got, want[v.EntryID(i)])
		}
	}
}

// TestArenaGrowsByChunksAndNothingIsCopied checks the record arena: records never span two chunks, a record larger than a
// chunk gets one of its own, chunks double from the first size to the limit, a chunk is never replaced when the arena
// grows (a slice into it stays valid), and every record reads back whole.
func TestArenaGrowsByChunksAndNothingIsCopied(t *testing.T) {
	tr := newTranscript(t, 2)
	sizes := []int{100, 5000, 9000, 20000, 60000, 3 * arenaChunk, 70000, 70000, 300, 130000, 40000}
	for _, n := range sizes {
		tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"`+strings.Repeat("x", n)+`"}],"timestamp":1}`, "")
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	first := st.chainList[0].record(0)
	// Appending to a loaded chain adds a record without moving the earlier ones.
	tr.add("pi.system", `{"role":"system","content":[{"type":"text","text":"`+strings.Repeat("y", 90000)+`"}],"timestamp":1}`, "")
	row := tr.db.entries[len(tr.db.entries)-1]
	if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
		t.Fatal(err)
	}
	ch := st.chainList[0]
	if len(ch.arena) < 4 {
		t.Fatalf("%d chunks for %d bytes", len(ch.arena), ch.ArenaBytes())
	}
	for i := range ch.ents {
		e := ch.ents[i]
		if int(e.off)+int(e.n) > cap(ch.arena[e.chunk]) {
			t.Fatalf("record %d spans past its chunk", i)
		}
		if got, want := ch.record(i), tr.db.entries[i].rec; !bytes.Equal(got, want) {
			t.Fatalf("record %d reads back %d bytes, appended %d", i, len(got), len(want))
		}
	}
	if &first[0] != &ch.record(0)[0] {
		t.Fatal("growing the arena moved an earlier record")
	}
	if cap(ch.arena[0]) != arenaFirstChunk || cap(ch.arena[1]) > 2*arenaFirstChunk {
		t.Fatalf("chunks start at %d then %d, want %d doubling", cap(ch.arena[0]), cap(ch.arena[1]), arenaFirstChunk)
	}
}

// TestHeadMarkerAndEditForceRederivation covers the two events that cannot extend a cache: a new head marker and
// an edit of an earlier entry.
func TestHeadMarkerAndEditForceRederivation(t *testing.T) {
	tr := newTranscript(t, 2)
	var firsts []int64
	for i := range 6 {
		firsts = append(firsts, tr.turn(i))
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	sync := func() {
		for _, row := range tr.db.entries {
			if row.id > st.chainList[0].LastID() {
				if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	tr.compact(firsts[4])
	sync()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !v.HasHead() || v.NumEntries() != 1+8 || st.rebuilt != 2 {
		t.Fatalf("head %v entries %d rebuilt %d", v.HasHead(), v.NumEntries(), st.rebuilt)
	}
	// An edit entry that omits the kept user entry of turn 4.
	tr.add("pi.system", "", `,"edits":[{"target":`+strconv.FormatInt(firsts[4], 10)+`,"action":"omit"}]`)
	sync()
	v, err = tr.db.context(t, st, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.rebuilt != 3 {
		t.Fatalf("an edit extended the cache: rebuilt %d", st.rebuilt)
	}
	if n := v.NumContributions(1); n != 0 {
		t.Fatalf("omitted entry still contributes %d messages", n)
	}
	// Appending after the edit extends again.
	tr.turn(7)
	sync()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	if st.rebuilt != 3 || st.extended == 0 {
		t.Fatalf("rebuilt %d extended %d", st.rebuilt, st.extended)
	}
}

// TestForkReadsParentRangeThroughAt checks cold derivation of a fork: child entries then parent entries through
// parent.at (spec section 2.1), reading only the parent's active range.
func TestForkReadsParentRangeThroughAt(t *testing.T) {
	tr := newTranscript(t, 2)
	var at int64
	for i := range 200 {
		first := tr.turn(i)
		if i == 150 {
			at = first + 3 // the final answer of turn 150
		}
	}
	// A parent head marker at turn 100 bounds the range the fork inherits.
	tr.nextID += 0
	child := tr.nextID
	tr.nextID++
	tr.db.convs[child] = AppendConversation(nil, ConvRecord{ID: child, HasParent: true, ParentConv: 2, ParentAt: at})
	ct := &transcript{t: t, db: tr.db, conv: child, nextID: tr.nextID, seq: tr.seq}
	ct.turn(1)
	st := NewStore()
	v, err := tr.db.context(t, st, child, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 151 parent turns (0..150) + 1 child turn, no marker: everything visible.
	if want := 151*4 + 4; v.NumEntries() != want {
		t.Fatalf("%d entries, want %d", v.NumEntries(), want)
	}
	// Cut at the fork point: the parent's later entries are invisible.
	if v.EntryID(v.NumEntries()-5) != at {
		t.Fatalf("entry before the child's turn is %d, want the fork point %d", v.EntryID(v.NumEntries()-5), at)
	}
	if owner, _, found, need := st.VisibleEntry(child, at); !found || need != nil || owner != 2 {
		t.Fatalf("VisibleEntry(child, at) = %d %v %v", owner, found, need)
	}
	if _, _, found, _ := st.VisibleEntry(child, at+1); found {
		t.Fatal("an entry after the fork point is visible from the child")
	}
	if _, err := tr.db.context(t, NewStore(), child, at+1); err == nil {
		t.Fatal("context cut at an invisible entry succeeded")
	} else if err.Error() != "Entry "+strconv.FormatInt(at+1, 10)+" is not visible from conversation "+strconv.FormatInt(child, 10) {
		t.Fatalf("error text %q", err)
	}
}

// TestIndexMemory checks H-D4: the fixed-width index stays small beside the arena it indexes.
func TestIndexMemory(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 875 { // 3,500 entries
		tr.turn(i)
	}
	st := NewStore()
	if _, err := tr.db.context(t, st, 2, 0); err != nil {
		t.Fatal(err)
	}
	ch := st.chainList[0]
	perEntry := float64(ch.IndexBytes()) / float64(ch.Len())
	if perEntry > 96 {
		t.Fatalf("index uses %.0f bytes per entry", perEntry)
	}
	t.Logf("%d entries: arena %d bytes, index %d bytes (%.0f per entry)", ch.Len(), ch.ArenaBytes(), ch.IndexBytes(), perEntry)
}

type scanDocs map[int64][]DocSource

func (s scanDocs) ScanConversationDocs(conv int64, at DocPoint) []DocSource {
	// One list per scope; the test's documents ignore the point.
	return s[conv]
}

func TestPrepareForkDocumentCopies(t *testing.T) {
	docs := scanDocs{
		2: {
			{ID: 10, Kind: []byte("pi.inbox"), Fork: ForkInitial},
			{ID: 11, Kind: []byte("pi.live"), Fork: ForkAsOf},
			{ID: 12, Kind: []byte("note"), Key: []byte("k1"), HasKey: true, Fork: ForkCurrent},
			{ID: 13, Kind: []byte("memo"), Fork: ForkAsOf},
		},
		3: {{ID: 20, Kind: []byte("memo"), Fork: ForkCurrent}, {ID: 21, Kind: []byte("other"), Fork: ForkCurrent}},
	}
	next := int64(100)
	mint := func() int64 { next++; return next }
	// Fork point owned by conversation 2, parent 3: asOf documents of 2 first, then current documents of 3.
	copies, err := PrepareForkDocumentCopies(docs, 3, 2, 55, 9, mint)
	if err == nil {
		t.Fatalf("two sources for memo selected: %+v", copies)
	}
	var dup *DuplicateForkCopyError
	if !errors.As(err, &dup) || string(dup.Member) != "memo" {
		t.Fatalf("err = %v", err)
	}
	if next != 103 {
		t.Fatalf("minted up to %d; ids are minted before the duplicate check", next)
	}
	delete(docs, 3)
	docs[3] = []DocSource{{ID: 21, Kind: []byte("other"), Fork: ForkCurrent}}
	next = 100
	copies, err = PrepareForkDocumentCopies(docs, 3, 2, 55, 9, mint)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, c := range copies {
		got = append(got, c.NewID, c.Source.ID)
	}
	want := []int64{101, 11, 102, 13, 103, 21}
	if len(got) != len(want) {
		t.Fatalf("copies %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("copies %v, want %v", got, want)
		}
	}
	if !copies[0].At.Current && copies[0].At.Seq != 55 || !copies[2].At.Current {
		t.Fatalf("points %+v", copies)
	}
	if string(AddressID([]byte("note"), 9, []byte("k\"1"), true)) != `["note","conversation",9,"k\"1"]` {
		t.Fatal("address id")
	}
}

// BenchmarkWarmAppendContext is one warm eight-entry turn at 3,500 entries of history: append each entry the way a
// commit applies it and derive the context after each.
func BenchmarkWarmAppendContext(b *testing.B) {
	tr := newTranscript(b, 2)
	for i := range 875 {
		tr.turn(i)
	}
	st := NewStore()
	if _, err := tr.db.context(b, st, 2, 0); err != nil {
		b.Fatal(err)
	}
	n := len(tr.db.entries)
	for i := 0; i < b.N; i++ {
		tr.turn(1000 + i)
	}
	rows := tr.db.entries[n:]
	b.ReportAllocs()
	b.ResetTimer()
	for _, row := range rows {
		if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
			b.Fatal(err)
		}
		if _, need, _ := st.Context(2, 0); need != nil {
			b.Fatal("read")
		}
	}
}

func BenchmarkColdOpen3500(b *testing.B) {
	tr := newTranscript(b, 2)
	for i := range 875 {
		tr.turn(i)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		st := NewStore()
		if _, err := tr.db.context(b, st, 2, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRowColumns(t *testing.T) {
	rec, err := AppendEntryRecord(nil, []byte(`{"kind":"pi.reset","head":"self"}`), 41, 7, 9, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec) != `{"kind":"pi.reset","id":41,"conversationId":7,"head":41,"byTaskId":9}` {
		t.Fatalf("record %s", rec)
	}
	row, err := NewEntryRow(rec, 12)
	if err != nil || row.ID != 41 || row.Conv != 7 || !row.HasHead || row.Head != 41 || row.Seq != 12 {
		t.Fatalf("%+v %v", row, err)
	}
	plain, _ := AppendEntryRecord(nil, []byte(`{"kind":"pi.user","head":null}`), 5, 2, 0, false)
	if row, _ := NewEntryRow(plain, 1); row.HasHead || string(plain) != `{"kind":"pi.user","id":5,"conversationId":2,"head":null}` {
		t.Fatalf("a null head is not a head column: %+v %s", row, plain)
	}
	conv := AppendConversation(nil, ConvRecord{ID: 9, HasParent: true, ParentConv: 2, ParentAt: 5, HasOwner: true, OwnerConv: 2, OwnerTask: 6})
	if string(conv) != `{"id":9,"parent":{"conversationId":2,"at":5},"owner":{"conversationId":2,"taskId":6}}` {
		t.Fatalf("conversation %s", conv)
	}
	crow, err := NewConversationRow(conv)
	if err != nil || !crow.HasOwner || crow.OwnerConv != 2 || crow.OwnerTask != 6 {
		t.Fatalf("%+v %v", crow, err)
	}
}

// A conversation read while it was empty answers a bound query that stops being true when an entry commits: the
// entries appended afterwards must show in the next context, whether or not the chain had been loaded.
func TestEntriesAppendedAfterAnEmptyReadAreInTheNextContext(t *testing.T) {
	tr := newTranscript(t, 2)
	st := NewStore()
	v, err := tr.db.context(t, st, 2, 0)
	if err != nil || v.NumEntries() != 0 {
		t.Fatalf("empty conversation: %d entries, %v", v.NumEntries(), err)
	}
	id := tr.add("pi.user", `{"role":"user","content":"hi","timestamp":1}`, "")
	row := tr.db.entries[len(tr.db.entries)-1]
	if err := st.Append(2, id, row.seq, row.rec); err != nil {
		t.Fatal(err)
	}
	v, err = tr.db.context(t, st, 2, 0)
	if err != nil || v.NumEntries() != 1 {
		t.Fatalf("after an append: %d entries, %v", v.NumEntries(), err)
	}
}

// A chain loaded for an earlier cutoff holds entries only through that cutoff: a later read at a higher cutoff must
// find the entries above it (context as of entry 8 after the context as of entry 7).
func TestContextAtAHigherCutoffAfterALowerOne(t *testing.T) {
	tr := newTranscript(t, 2)
	var ids []int64
	for range 3 {
		ids = append(ids, tr.add("pi.user", `{"role":"user","content":"x","timestamp":1}`, ""))
	}
	st := NewStore()
	for i, at := range ids {
		v, err := tr.db.context(t, st, 2, at)
		if err != nil || v.NumEntries() != i+1 {
			t.Fatalf("context at %d: %d entries, %v", at, v.NumEntries(), err)
		}
	}
}

// TestABoundedChainIsExtendedByTheEntriesAboveIt checks that a host that reads contexts cut off at explicit entries (a
// model_context range) and is never told of commits (no Append) reads only the entries above the chain it holds, and
// derives what a store with nothing loaded derives.
func TestABoundedChainIsExtendedByTheEntriesAboveIt(t *testing.T) {
	tr := newTranscript(t, 2)
	for i := range 20 {
		tr.turn(i)
	}
	st := NewStore()
	tail := tr.nextID - 1
	if _, err := tr.db.context(t, st, 2, tail); err != nil {
		t.Fatal(err)
	}
	for round := range 3 {
		for i := range 5 {
			tr.turn(100 + round*5 + i)
		}
		tail = tr.nextID - 1
		before := tr.db.rowsRead
		got, err := tr.db.context(t, st, 2, tail)
		if err != nil {
			t.Fatal(err)
		}
		// 20 new entries, and the bounds probe of the new cutoff.
		if rows := tr.db.rowsRead - before; rows > 20+3 {
			t.Fatalf("round %d: read %d rows for 20 new entries", round, rows)
		}
		want, err := tr.db.context(t, NewStore(), 2, tail)
		if err != nil {
			t.Fatal(err)
		}
		if formatView(got) != formatView(want) {
			t.Fatalf("round %d: the extended context differs from a fresh derivation", round)
		}
		checkMessageIndex(t, got)
	}
	if st.rebuilt != 1 {
		t.Fatalf("rebuilt %d times, want once and then extended", st.rebuilt)
	}
}

// A chain that holds a bounded range (the context at a cutoff) keeps it when the Session commits a later entry: a request in
// flight holds the view derived from the chain across its hops (a compaction summarizes the context at its tail while other
// jobs commit), so Append must not reset the chain under it. The next use for a later entry extends the chain and reads the
// entries committed in between.
func TestAnAppendDoesNotInvalidateAViewOfABoundedChain(t *testing.T) {
	tr := newTranscript(t, 2)
	var cut int64
	for i := range 12 {
		first := tr.turn(i)
		if i == 8 {
			cut = first
		}
	}
	st := NewStore()
	view, err := tr.db.context(t, st, 2, cut)
	if err != nil {
		t.Fatal(err)
	}
	wantEntries, wantMessages := view.NumEntries(), view.NumMessages()
	first := view.EntryID(0)
	for i := 12; i < 15; i++ {
		tr.turn(i)
		for _, row := range tr.db.entries[len(tr.db.entries)-4:] {
			if err := st.Append(2, row.id, row.seq, row.rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if view.NumEntries() != wantEntries || view.NumMessages() != wantMessages || view.EntryID(0) != first || view.CutOf(cut) < 0 {
		t.Fatal("a commit invalidated the view of the bounded chain")
	}
	tail := tr.nextID - 1
	got, err := tr.db.context(t, st, 2, tail)
	if err != nil {
		t.Fatal(err)
	}
	want, err := tr.db.context(t, NewStore(), 2, tail)
	if err != nil {
		t.Fatal(err)
	}
	if formatView(got) != formatView(want) {
		t.Fatal("the context after the commits differs from a fresh derivation")
	}
	checkMessageIndex(t, got)
}
