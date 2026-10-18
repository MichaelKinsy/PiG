// SPDX-License-Identifier: MIT

package abi

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func sampleStep() *Step {
	return &Step{
		Commits: []Commit{
			{Seq: 7, Stmts: []Stmt{
				{SQL: 1, Params: []Value{Null(), Int(-1 << 53), Float(math.Copysign(0, -1)), Text("é\u2028"), Blob([]byte{0, 1, 255}), Text("")}},
				{SQL: 2},
			}},
			{Seq: -1},
			{Seq: 8, Stmts: []Stmt{{SQL: 65535, Params: []Value{Float(math.NaN())}}}},
		},
		Reads:   []Read{{ID: 4, SQL: 9, Params: []Value{Int(3)}}},
		Notices: []Notice{{Kind: NoticeReport, Payload: []byte(`{"a":1}`)}, {Kind: NoticeSidecar}},
		Effects: []Effect{{ID: 1, Kind: EffectTimer, Payload: TimerEffect(1, 12.5, TimerDurable)}, {ID: 2, Kind: EffectLiveness, Payload: F64Payload(-1)}},
	}
}

func TestStepRoundTrip(t *testing.T) {
	for _, s := range []*Step{{}, sampleStep(), {Status: StatusRejected, Err: []byte(`{"name":"X"}`)}, {Status: StatusFatal, Err: nil}} {
		wire := AppendStep(nil, s)
		got, err := DecodeStep(wire)
		if err != nil {
			t.Fatal(err)
		}
		if again := AppendStep(nil, got); !bytes.Equal(again, wire) {
			t.Fatalf("round trip changed the bytes:\n%x\n%x", wire, again)
		}
		if got.Status != s.Status || len(got.Commits) != len(s.Commits) || len(got.Effects) != len(s.Effects) {
			t.Fatalf("decoded step differs: %+v", got)
		}
	}
}

func TestStepLengthPrefixes(t *testing.T) {
	wire := AppendStep(nil, sampleStep())
	if size := int(wire[0]) | int(wire[1])<<8 | int(wire[2])<<16 | int(wire[3])<<24; size != len(wire)-4 {
		t.Fatalf("size %d, want %d", size, len(wire)-4)
	}
	for i := range wire {
		if _, err := DecodeStep(wire[:i]); err == nil {
			t.Fatalf("a step truncated to %d bytes decoded", i)
		}
	}
	if _, err := DecodeStep(append(wire, 0)); err == nil {
		t.Fatal("trailing byte accepted")
	}
}

func TestRowsRoundTrip(t *testing.T) {
	in := []ReadRows{
		{ID: 1, Cols: 2, Rows: [][]Value{{Int(1), Text("a")}, {Null(), Blob([]byte("b"))}}},
		{ID: 2, Cols: 1},
	}
	got, err := DecodeRows(AppendRows(nil, in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || len(got[0].Rows) != 2 || !got[0].Rows[1][1].Equal(Blob([]byte("b"))) || len(got[1].Rows) != 0 {
		t.Fatalf("rows changed: %+v", got)
	}
}

// The host reuses the payload buffer of a rows event for the next event, and a record the core keeps must not point into
// it: the decoded values are copies, and many rows share small chunks, not one allocation per row or per answer.
func TestDecodeRowsDoesNotAliasThePayload(t *testing.T) {
	var rows [][]Value
	for i := range 600 {
		rows = append(rows, []Value{Int(int64(i)), Text(strings.Repeat(string(rune('a'+i%26)), 100+i%7))})
	}
	wire := AppendRows(nil, []ReadRows{{ID: 7, Cols: 2, Rows: rows}})
	got, err := DecodeRows(wire)
	if err != nil {
		t.Fatal(err)
	}
	for i := range wire {
		wire[i] = 0xff
	}
	for i, r := range got[0].Rows {
		if want := strings.Repeat(string(rune('a'+i%26)), 100+i%7); string(r[1].Bytes) != want {
			t.Fatalf("row %d changed when the payload was overwritten", i)
		}
		if cap(r[1].Bytes) != len(r[1].Bytes) {
			t.Fatalf("row %d has room to grow into its neighbour", i)
		}
	}
	if cap(got[0].Rows[0][1].Bytes) >= rowsChunk || &got[0].Rows[0][1].Bytes[0] == &got[0].Rows[1][1].Bytes[0] {
		t.Fatal("values are not copies of their own length")
	}
}

// ABI section 5.3: the builder records the order of its notices and effects; the wire carries it only when a step has both.
func TestStepOrderRoundTripsAndIsOmittedForOneRegion(t *testing.T) {
	b := NewBuilder()
	b.Effect(EffectHops, AppendHops(nil, Hops{N: 3}))
	b.Notice(NoticeAPIResult, []byte("1"))
	b.Effect(EffectHops, AppendHops(nil, Hops{N: 2}))
	got, err := DecodeStep(AppendStep(nil, b.Step()))
	if err != nil {
		t.Fatal(err)
	}
	want := []OrderRef{{OrderEffect, 0}, {OrderNotice, 0}, {OrderEffect, 1}}
	if len(got.Order) != len(want) {
		t.Fatalf("order = %v, want %v", got.Order, want)
	}
	for i := range want {
		if got.Order[i] != want[i] {
			t.Fatalf("order = %v, want %v", got.Order, want)
		}
	}
	b.Reset()
	b.Effect(EffectHops, AppendHops(nil, Hops{N: 1}))
	b.Effect(EffectHops, AppendHops(nil, Hops{N: 1}))
	got, err = DecodeStep(AppendStep(nil, b.Step()))
	if err != nil || len(got.Order) != 0 {
		t.Fatalf("a one-region step carries no order: %v %v", got.Order, err)
	}
}

func TestDecodeStepRejectsABadOrder(t *testing.T) {
	b := NewBuilder()
	b.Effect(EffectHops, AppendHops(nil, Hops{N: 1}))
	b.Notice(NoticeAPIResult, []byte("1"))
	wire := AppendStep(nil, b.Step())
	n := len(wire)
	incomplete := append([]byte(nil), wire[:n-2*3-2]...) // drop the two entries and the count
	incomplete = append(incomplete, 1, 0, OrderEffect, 0, 0)
	binary.LittleEndian.PutUint32(incomplete, uint32(len(incomplete)-4))
	if _, err := DecodeStep(incomplete); err == nil {
		t.Fatal("an order that lists 1 of 2 items must be rejected")
	}
	missing := append([]byte(nil), wire[:n-2*3-2]...)
	missing = append(missing, 2, 0, OrderEffect, 0, 0, OrderNotice, 5, 0)
	binary.LittleEndian.PutUint32(missing, uint32(len(missing)-4))
	if _, err := DecodeStep(missing); err == nil {
		t.Fatal("an order that names a missing notice must be rejected")
	}
}

// Every event kind decodes: hopped carries the u32 effect ID of its chain (section 5.3), seal has no payload (event 19).
func TestDecodeEventHoppedAndSeal(t *testing.T) {
	ev, err := DecodeEvent(EventHopped, 1, Event{Kind: EventHopped, ID: 42}.Wire())
	if err != nil || ev.ID != 42 {
		t.Fatalf("hopped: %+v, %v", ev, err)
	}
	if _, err := DecodeEvent(EventSeal, 1, nil); err != nil {
		t.Fatalf("seal: %v", err)
	}
}

// StepSize is the exact length of the wire form, for every region.
func TestStepSizeIsTheWireLength(t *testing.T) {
	steps := []*Step{
		{},
		{Status: StatusRejected, Err: ErrorJSON("Error", "no")},
		{
			Commits: []Commit{{Seq: 3, Stmts: []Stmt{{SQL: 1, Params: []Value{Null(), Int(7), Float(1.5), Text("abc"), Blob([]byte{1, 2})}}, {SQL: 2}}}},
		},
		{Reads: []Read{{ID: 1, SQL: 4, Params: []Value{Int(1), Text("x")}}}},
		{
			Notices: []Notice{{Kind: NoticeAPIResult, Payload: []byte("{}")}},
			Effects: []Effect{{ID: 9, Kind: EffectHook, Payload: []byte(`{"a":1}`)}, {ID: 10, Kind: EffectHops, Payload: U32Payload(2)}},
			Order:   []OrderRef{{Region: OrderEffect, Index: 0}, {Region: OrderNotice, Index: 0}, {Region: OrderEffect, Index: 1}},
		},
		{Notices: []Notice{{Kind: NoticePublished, Payload: []byte("[]")}}, Order: []OrderRef{{Region: OrderNotice, Index: 0}}},
	}
	for i, s := range steps {
		if got, want := StepSize(s), len(AppendStep(nil, s)); got != want {
			t.Errorf("step %d: StepSize %d, wire length %d", i, got, want)
		}
	}
}

// A binding hands the payloads of big or more bytes to its host as parts of their own, not copied into its buffer: the
// parts concatenate to AppendStep's bytes (size prefix included), the buffer holds StepSizeInline bytes, and a payload
// part aliases the payload. Payloads at the front, the back, side by side and below the bound all keep the wire form.
func TestStepPartsConcatenateToTheWireForm(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 16)
	bigger := bytes.Repeat([]byte("y"), 40)
	steps := []*Step{
		{},
		sampleStep(),
		{Effects: []Effect{{ID: 1, Kind: EffectModelContext, Payload: big}}},
		{Notices: []Notice{{Kind: NoticeReport, Payload: bigger}}, Effects: []Effect{{ID: 1, Kind: EffectModelContext, Payload: big}, {ID: 2, Kind: EffectModelContext, Payload: bigger}, {ID: 3, Kind: EffectHops, Payload: U32Payload(2)}}, Order: []OrderRef{{Region: OrderEffect, Index: 0}, {Region: OrderNotice, Index: 0}, {Region: OrderEffect, Index: 1}, {Region: OrderEffect, Index: 2}}},
		{Status: StatusRejected, Err: ErrorJSON("Error", "no"), Effects: []Effect{{ID: 1, Kind: EffectModelContext, Payload: bigger[:15]}}},
	}
	for i, s := range steps {
		want := AppendStep(nil, s)
		prefix := []byte("keep")
		buf, parts := AppendStepParts(prefix, s, 16, nil)
		if !bytes.Equal(buf[:4], prefix) || len(buf)-4 != StepSizeInline(s, 16) {
			t.Fatalf("step %d: the buffer holds %d bytes after its prefix, StepSizeInline %d", i, len(buf)-4, StepSizeInline(s, 16))
		}
		var got []byte
		for _, p := range parts {
			if p.Ext != nil {
				if len(p.Ext) < 16 {
					t.Fatalf("step %d: a payload of %d bytes is a part of its own", i, len(p.Ext))
				}
				if &p.Ext[0] != &big[0] && &p.Ext[0] != &bigger[0] {
					t.Fatalf("step %d: a payload part is a copy", i)
				}
				got = append(got, p.Ext...)
				continue
			}
			if p.End <= p.Off || p.Off < 4 {
				t.Fatalf("step %d: part %+v", i, p)
			}
			got = append(got, buf[p.Off:p.End]...)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("step %d: the parts concatenate to\n%x\nnot\n%x", i, got, want)
		}
	}
}

// HopsSize is the length of the wire form, so a binding sizes a hops payload's buffer before it writes it.
func TestHopsSizeIsTheWireLength(t *testing.T) {
	commit := func(seq int64, params ...Value) Commit {
		return Commit{Seq: seq, Stmts: []Stmt{{SQL: 3, Params: params}, {SQL: 4}}}
	}
	for _, h := range []Hops{
		{N: 3},
		{N: 5, At: 2, Commits: []Commit{commit(1)}},
		{N: 6, At: 1, Commits: []Commit{commit(1, Int(7), Text("héllo"), Null(), Blob(make([]byte, 300))), commit(2, Int(-1))}},
	} {
		wire := AppendHops(nil, h)
		if got := HopsSize(h); got != len(wire) {
			t.Errorf("HopsSize = %d, the wire form is %d bytes (%d commits)", got, len(wire), len(h.Commits))
		}
		if _, err := DecodeHops(wire); err != nil {
			t.Errorf("decode: %v", err)
		}
	}
}

// ABI section 5.3: a chain carries commits only with 0 < at < n, and at is 0 without them. A host that took a malformed chain
// would apply its commits at a hop that does not exist (or never), so DecodeHops refuses it and the host discards the handle.
func TestDecodeHopsRejectsAMalformedChain(t *testing.T) {
	c := Commit{Seq: 1, Stmts: []Stmt{{SQL: 3, Params: []Value{Int(7)}}}}
	for name, wire := range map[string][]byte{
		"n = 0":               AppendHops(nil, Hops{}),
		"commits with at = 0": AppendHops(nil, Hops{N: 5, Commits: []Commit{c}}),
		"at = n":              AppendHops(nil, Hops{N: 5, At: 5, Commits: []Commit{c}}),
		"at past n":           AppendHops(nil, Hops{N: 5, At: 6, Commits: []Commit{c}}),
		"at without commits":  AppendHops(nil, Hops{N: 5, At: 2}),
		"truncated commit":    AppendHops(nil, Hops{N: 5, At: 2, Commits: []Commit{c}})[:20],
		"no at":               binary.LittleEndian.AppendUint32(nil, 3),
	} {
		if _, err := DecodeHops(wire); err == nil {
			t.Errorf("%s: DecodeHops accepted %x", name, wire)
		}
	}
	h, err := DecodeHops(AppendHops(nil, Hops{N: 5, At: 4, Commits: []Commit{c, c}}))
	if err != nil || h.N != 5 || h.At != 4 || len(h.Commits) != 2 || h.Commits[1].Stmts[0].Params[0].Int != 7 {
		t.Fatalf("a chain with at = n-1 and two commits: %+v %v", h, err)
	}
}

// decoders returns DecodeRows and DecodeAnswer bound to the payloads that carry rows in each.
func decoders(rows [][]Value) map[string]struct {
	payload []byte
	decode  func([]byte) error
} {
	cols := uint8(len(rows[0]))
	return map[string]struct {
		payload []byte
		decode  func([]byte) error
	}{
		"DecodeRows":   {AppendRows(nil, []ReadRows{{ID: 3, Cols: cols, Rows: rows}}), func(b []byte) error { _, err := DecodeRows(b); return err }},
		"DecodeAnswer": {AppendAnswer(nil, cols, rows), func(b []byte) error { _, err := DecodeAnswer(b); return err }},
	}
}

// bytesPerDecode is the heap one decode of payload allocates, averaged over a few runs.
func bytesPerDecode(t *testing.T, payload []byte, decode func([]byte) error) uint64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	const runs = 50
	for range runs {
		if err := decode(payload); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// A record the core keeps from a small answer points into the chunk its text was copied to; under TinyGo's collector one
// interior pointer keeps the whole chunk. A chunk of rowsChunk for a 58-byte answer therefore pinned 16 KiB behind each such
// record, and which turn held one decided the live set the count gate sampled (liveGrowthPerTurn +3,904 B/turn over six
// turns, one 16 KiB chunk). The chunk is never larger than the payload that fills it.
func TestDecodeKeepsAtMostThePayloadBehindASmallAnswer(t *testing.T) {
	for name, d := range decoders([][]Value{{Int(1), Text("some text")}, {Int(2), Blob([]byte("and a blob"))}}) {
		if per := bytesPerDecode(t, d.payload, d.decode); per > 2048 {
			t.Errorf("%s of a %d-byte answer allocates %d bytes: a chunk larger than the payload", name, len(d.payload), per)
		}
	}
}

// The tail of an answer longer than rowsChunk is a small answer too: a value that does not fit the first chunk gets a chunk
// as long as what is left to copy, not another rowsChunk, so a record kept from the tail does not pin 16 KiB behind 500 bytes.
func TestDecodeKeepsAtMostTheRestBehindTheTailOfALargeAnswer(t *testing.T) {
	rows := [][]Value{{Int(1), Text(strings.Repeat("a", rowsChunk-100))}, {Int(2), Blob([]byte(strings.Repeat("b", 500)))}}
	for name, d := range decoders(rows) {
		if per := bytesPerDecode(t, d.payload, d.decode); per > uint64(len(d.payload))+2048 {
			t.Errorf("%s of a %d-byte answer allocates %d bytes: the tail got a chunk longer than the rest of the payload", name, len(d.payload), per)
		}
	}
}

// A large page is copied into chunks of at most rowsChunk, not one allocation as long as the page: a record kept from it
// pins one chunk, not the whole page. Same rows with one-byte values take one chunk, so the difference counts the chunks.
func TestDecodeSplitsALargeAnswerIntoChunks(t *testing.T) {
	// 1000-byte values leave a chunk 384 bytes short of full (16 values in 16 KiB), so the next chunk never starts where the last
	// value of the one before ends: two values sit end to end only when they share a chunk.
	const values, size = 64, 1000
	rows := make([][]Value, values)
	for i := range rows {
		rows[i] = []Value{Int(int64(i)), Text(strings.Repeat("c", size))}
	}
	texts := map[string][][]byte{}
	decodedRows, err := DecodeRows(AppendRows(nil, []ReadRows{{ID: 3, Cols: 2, Rows: rows}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range decodedRows[0].Rows {
		texts["DecodeRows"] = append(texts["DecodeRows"], row[1].Bytes)
	}
	answer, err := DecodeAnswer(AppendAnswer(nil, 2, rows))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range answer {
		texts["DecodeAnswer"] = append(texts["DecodeAnswer"], row[1].Bytes)
	}
	// The chunks are read from where the copies sit, not from allocation counts, so the check holds under TinyGo too, whose
	// testing.AllocsPerRun and MemStats.Mallocs report 0.
	for name, vs := range texts {
		chunks, run := 1, len(vs[0])
		for i := 1; i < len(vs); i++ {
			if reflect.ValueOf(vs[i]).Pointer() == reflect.ValueOf(vs[i-1]).Pointer()+uintptr(len(vs[i-1])) {
				run += len(vs[i])
			} else {
				chunks, run = chunks+1, len(vs[i])
			}
			if run > rowsChunk {
				t.Fatalf("%s: a chunk holds %d bytes of values, more than rowsChunk %d", name, run, rowsChunk)
			}
		}
		if want := (values*size + rowsChunk - 1) / rowsChunk; chunks < want {
			t.Errorf("%s copied a %d-byte page into %d chunks, want at least %d", name, values*size, chunks, want)
		}
	}
}

// A small answer copies its values into a buffer sized to the answer, not a whole chunk: the core decodes about fourteen
// answers per warm turn, most of them a few hundred bytes, and each used to allocate 16 KiB.
func TestSmallAnswersAndRowsAllocateOnlyWhatTheyHold(t *testing.T) {
	blob := []byte(strings.Repeat("x", 300))
	answer := AppendAnswer(nil, 1, [][]Value{{Blob(blob)}})
	rows := AppendRows(nil, []ReadRows{{ID: 1, Cols: 1, Rows: [][]Value{{Blob(blob)}}}})
	perCall := func(decode func()) uint64 {
		const n = 200
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range n {
			decode()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / n
	}
	if got := perCall(func() { _, _ = DecodeAnswer(answer) }); got >= rowsChunk/2 {
		t.Fatalf("decoding a %d-byte answer allocates %d bytes", len(answer), got)
	}
	if got := perCall(func() { _, _ = DecodeRows(rows) }); got >= rowsChunk/2 {
		t.Fatalf("decoding a %d-byte rows payload allocates %d bytes", len(rows), got)
	}
}
