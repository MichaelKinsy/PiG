// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"math"
	"strconv"
)

// Message roles and stop reasons the index records. A role other than these four is code roleOther: derivation
// treats it like Pi does, as not a user, system, assistant or tool-result message.
const (
	roleOther uint8 = iota
	roleSystem
	roleUser
	roleAssistant
	roleToolResult
)

// Stop reasons that exclude an assistant message from model context (spec section 2.1, rule 9).
const (
	stopOther uint8 = iota
	stopAborted
	stopError
	stopDeferred
)

const (
	flagHead     uint8 = 1 << iota // the record has a numeric head, so it is a head marker
	flagHeadNull                   // the record has "head": null
	flagEdits                      // the record has an edits array
	flagModel                      // the record has a model array
)

// missingSpan marks a tool call id or tool result id that is absent or not a string.
const missingSpan = ^uint32(0)

// span is a tool call id held in the chain's ids: off is where its raw (still escaped) text starts. hash is the hash of the
// string the text decodes to, so two ids that differ are told apart without reading their bytes. The text lives in the index,
// not in the record, so a derivation does not read back an evicted record to compare ids.
type span struct {
	off, len uint32
	hash     uint32
}

// entry is one fixed-width index record of a chain (ADR-0001 D4).
type entry struct {
	id    int64
	seq   int64  // commit_seq of the commit that wrote the entry
	chunk uint32 // the arena chunk holding the record
	off   uint32 // start of the record in its chunk
	n     uint32 // the record's length in bytes
	msg0  uint32 // first model message in Chain.msgs; they end where the next entry's start
	kind  uint16
	flags uint8
}

// msgRef indexes one message of a record: its bytes, role, stop reason and tool call ids.
type msgRef struct {
	// off is relative to the start of record ent.
	off, len uint32
	ent      int32
	// nCalls is the number of tool calls of an assistant message, or 1 for a tool result's call id.
	nCalls uint32
	// calls indexes Chain.spans: nCalls spans of call ids (assistant) or one span (tool result).
	calls uint32
	role  uint8
	stop  uint8
}

// measure is the token estimate and context usage of one message. Compaction thresholds read them, so the message text is
// read once for each and then not again; known says which are filled in. Indexing does not compute them and a message
// nobody measures holds nothing: they live in blocks of measureBlock messages allocated when one of them is first measured.
type measure struct {
	tokens uint32
	usage  float64
	known  uint8
}

const measureBlock = 256

// headRec is a head marker of a chain.
type headRec struct {
	ent  int32
	head int64
}

// editRec is one context edit of a chain: entry ent's record carries an edit of target.
type editRec struct {
	ent     int32
	target  int64
	replace bool
	omit    bool
	msg0    uint32 // replacement messages are Chain.emsgs[msg0 : msg0+nMsg]
	nMsg    uint32
}

// Chain is one conversation's own entries: the stored record bytes in an append-only arena beside the fixed-width
// index. It holds the entries with an id of at least From, in id order.
type Chain struct {
	Conv int64
	// From is the smallest entry id whose absence is not known: every entry of the conversation with an id of at
	// least From is loaded. Zero means the whole conversation is loaded.
	From int64
	// To is the largest entry id the load covered: every entry of the conversation with an id in [From, To] is
	// loaded. A chain that follows the conversation's tip, as every chain the Session appends to, has To unbounded.
	To int64

	// epoch counts the resets: a message reference of an earlier epoch names bytes that are gone.
	epoch uint32

	// recBase and recEnt locate the record being indexed: the offsets the indexer sees are arena offsets.
	// ids holds the raw text of the tool call ids that spans index.
	ids     []byte
	recBase uint32
	recEnt  int32

	arena [][]byte // fixed-size chunks of record text: a record never spans two, and nothing large is copied on growth
	// chunkEnts[k] is the index of the first entry stored in chunk k. A chunk the store evicted is nil in arena and is read
	// back from storage when a record in it is read (evict, reload).
	chunkEnts []int32
	// reloaded lists the evicted chunks read back and still held, oldest first.
	reloaded []uint32
	st       *Store
	ents     []entry
	msgs     []msgRef // the model messages of the entries, in entry order
	emsgs    []msgRef // the replacement messages of edits, in entry order
	spans    []span
	meas     [][]measure // meas[i/measureBlock][i%measureBlock] is the measure of msgs[i]
	heads    []headRec
	edits    []editRec
}

func newChain(conv int64) *Chain { return &Chain{Conv: conv} }

// Len is the number of loaded entries.
func (c *Chain) Len() int { return len(c.ents) }

// LastID is the id of the newest loaded entry, or 0.
func (c *Chain) LastID() int64 {
	if len(c.ents) == 0 {
		return 0
	}
	return c.ents[len(c.ents)-1].id
}

// ArenaBytes is the size of the record text held.
func (c *Chain) ArenaBytes() int {
	n := 0
	for _, ch := range c.arena {
		n += len(ch)
	}
	return n
}

// The arena grows by chunks that double from arenaFirstChunk to arenaChunk, so a small conversation holds little. A
// record larger than a chunk gets a chunk of its own.
const (
	arenaFirstChunk = 16 << 10
	arenaChunk      = 128 << 10
)

// IndexBytes approximates the memory the fixed-width index uses.
func (c *Chain) IndexBytes() int {
	return len(c.ents)*40 + (len(c.msgs)+len(c.emsgs))*40 + len(c.spans)*16 + len(c.ids) + len(c.heads)*16 + len(c.edits)*32
}

// trimBelow drops the entries with an id below from and everything indexed for them, when they are at least half of the chain:
// it rebuilds the chain from the stored records of the entries that stay (reading an evicted chunk back, in order). A view of the
// chain is invalid afterwards (the epoch changes).
func (c *Chain) trimBelow(from int64) error {
	i := c.search(from)
	if i == 0 || c.st == nil || i < len(c.ents)/2 {
		// A derivation starts at the marker's head, so older entries cost only memory: dropping them once they are half of the
		// chain keeps it within twice its range and the rebuild's cost amortized.
		return nil
	}
	type kept struct {
		id, seq int64
		rec     []byte
	}
	list := make([]kept, 0, len(c.ents)-i)
	for j := i; j < len(c.ents); j++ {
		list = append(list, kept{c.ents[j].id, c.ents[j].seq, bytes.Clone(c.record(j))})
	}
	oldTo := c.To
	c.reset(from, oldTo)
	for _, k := range list {
		if err := c.appendRecord(c.st, k.id, k.seq, k.rec); err != nil {
			return err
		}
	}
	return nil
}

func (c *Chain) reset(from, to int64) {
	c.epoch++
	c.From, c.To = from, to
	if c.st != nil {
		c.st.resident -= c.residentBytes()
	}
	c.arena, c.chunkEnts, c.reloaded = nil, nil, nil
	c.ents = c.ents[:0]
	c.msgs = c.msgs[:0]
	c.emsgs = c.emsgs[:0]
	c.meas = nil
	c.spans = c.spans[:0]
	c.ids = c.ids[:0]
	c.heads = c.heads[:0]
	c.edits = c.edits[:0]
}

// search returns the position of the first loaded entry whose id is at least id.
func (c *Chain) search(id int64) int {
	lo, hi := 0, len(c.ents)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if c.ents[m].id < id {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}

// find returns the position of the loaded entry with the id, or -1.
func (c *Chain) find(id int64) int {
	i := c.search(id)
	if i < len(c.ents) && c.ents[i].id == id {
		return i
	}
	return -1
}

// read is the one place record text is read: n bytes at offset off of the stored record of entry ent. The slice stays
// valid until the next append to the chain.
func (c *Chain) read(ent int32, off, n uint32) []byte {
	e := &c.ents[ent]
	at := e.off + off
	chunk := c.arena[e.chunk]
	if chunk == nil {
		chunk = c.reload(e.chunk)
	}
	return chunk[at : at+n]
}

// record returns the stored record of entry i.
func (c *Chain) record(i int) []byte { return c.read(int32(i), 0, c.ents[i].n) }

// modelMessages returns the range of Chain.msgs holding the model messages of entry i.
func (c *Chain) modelMessages(i int) (from, to uint32) {
	to = uint32(len(c.msgs))
	if i+1 < len(c.ents) {
		to = c.ents[i+1].msg0
	}
	return c.ents[i].msg0, to
}

// msgAt returns a message by reference index: a model message, or when negative a replacement message of an edit.
func (c *Chain) msgAt(idx int32) *msgRef {
	if idx >= 0 {
		return &c.msgs[idx]
	}
	return &c.emsgs[^idx]
}

func (c *Chain) msgBytes(m *msgRef) []byte { return c.read(m.ent, m.off, m.len) }

func (c *Chain) spanBytes(sp span) []byte { return c.ids[sp.off : sp.off+sp.len] }

// latestHead returns the position in heads of the newest head marker with an id of at most upper, or -1.
func (c *Chain) latestHead(upper int64) int {
	// heads are in entry order, hence in id order.
	lo, hi := 0, len(c.heads)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if c.ents[c.heads[m].ent].id <= upper {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo - 1
}

// scanned is what scanning one stored record yields.
type scanned struct {
	id, conv, head int64
	hasID, hasConv bool
	kind           []byte
	flags          uint8
	byTask         int64
	hasByTask      bool
	model          []byte
	edits          []byte
	modelOff       int
	editsOff       int
}

func scanRecord(rec []byte) (scanned, error) {
	var out scanned
	s := newScanner(rec)
	it, err := s.object()
	if err != nil {
		return out, err
	}
	for it.next() {
		switch {
		case it.keyIs("id"):
			v, ok := intValue(it.value())
			out.id, out.hasID = v, ok
		case it.keyIs("conversationId"):
			v, ok := intValue(it.value())
			out.conv, out.hasConv = v, ok
		case it.keyIs("byTaskId"):
			out.byTask, out.hasByTask = intValue(it.value())
		case it.keyIs("kind"):
			out.kind = nil
			if v := it.value(); valueKind(v) == '"' {
				out.kind = v[1 : len(v)-1]
			}
		case it.keyIs("head"):
			out.flags &^= flagHead | flagHeadNull
			v := it.value()
			if valueKind(v) == 'l' {
				out.flags |= flagHeadNull
			} else if n, ok := intValue(v); ok {
				out.head = n
				out.flags |= flagHead
			}
		case it.keyIs("model"):
			out.flags &^= flagModel
			if v := it.value(); valueKind(v) == '[' {
				out.model, out.modelOff = v, it.vs
				out.flags |= flagModel
			}
		case it.keyIs("edits"):
			out.flags &^= flagEdits
			out.edits = nil
			if v := it.value(); valueKind(v) == '[' {
				out.edits, out.editsOff = v, it.vs
				out.flags |= flagEdits
			}
		}
	}
	if it.err != nil {
		return out, it.err
	}
	if !s.atEnd() {
		return out, ErrSyntax
	}
	return out, nil
}

// intValue reads a JSON number that is an integer within the safe range.
func intValue(v []byte) (int64, bool) {
	if valueKind(v) != 'n' {
		return 0, false
	}
	if n, err := strconv.ParseInt(string(v), 10, 64); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(string(v), 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// internKind returns the code of a kind string, adding it to the store's table.
func (st *Store) internKind(raw []byte) uint16 {
	var buf [64]byte
	k := raw
	if hasEscape(raw) {
		k = unescape(buf[:0], raw)
	}
	for i, known := range st.kinds {
		if bytes.Equal(known, k) {
			return uint16(i + 1)
		}
	}
	if len(st.kinds) >= math.MaxUint16-1 {
		return 0
	}
	st.kinds = append(st.kinds, append([]byte(nil), k...))
	st.kindStrings = append(st.kindStrings, string(k))
	return uint16(len(st.kinds))
}

// KindName returns the kind string of a kind code; code 0 is the empty (absent) kind.
func (st *Store) kindName(code uint16) []byte {
	if code == 0 || int(code) > len(st.kinds) {
		return nil
	}
	return st.kinds[code-1]
}

// kindString is the kind string of a kind code, allocated once when the kind is first seen.
func (st *Store) kindString(code uint16) string {
	if code == 0 || int(code) > len(st.kindStrings) {
		return ""
	}
	return st.kindStrings[code-1]
}

// appendRecord copies a stored record into the arena and indexes it. seq is the commit_seq of the entry's commit.
// The entry id must exceed every loaded id of the chain, which holds for rows read in id order and for entries
// appended in commit order (ADR-0001 E-D6b).
func (c *Chain) appendRecord(st *Store, id, seq int64, rec []byte) error {
	sc, err := scanRecord(rec)
	if err != nil {
		return err
	}
	if sc.hasID && sc.id != id {
		return ErrShape
	}
	if n := len(c.ents); n > 0 && c.ents[n-1].id >= id {
		return jsonError("entry ids must ascend within a conversation")
	}
	// The record goes at the end of the newest chunk, or into a new one when it does not fit.
	chunksMark, lastMark := len(c.arena), 0
	if chunksMark > 0 {
		lastMark = len(c.arena[chunksMark-1])
	}
	if chunksMark == 0 || cap(c.arena[chunksMark-1])-lastMark < len(rec) {
		size := arenaFirstChunk
		if chunksMark > 0 {
			size = min(2*cap(c.arena[chunksMark-1]), arenaChunk)
		}
		c.arena = append(c.arena, make([]byte, 0, max(size, len(rec))))
		c.chunkEnts = append(c.chunkEnts, int32(len(c.ents)))
	}
	chunk := uint32(len(c.arena) - 1)
	base := uint32(len(c.arena[chunk]))
	msgMark, emsgMark, spanMark, editMark, idMark := len(c.msgs), len(c.emsgs), len(c.spans), len(c.edits), len(c.ids)
	c.arena[chunk] = append(c.arena[chunk], rec...)
	ent := entry{id: id, seq: seq, chunk: chunk, off: base, n: uint32(len(rec)), msg0: uint32(len(c.msgs)), flags: sc.flags}
	ent.kind = st.internKind(sc.kind)
	idx := int32(len(c.ents))
	c.recBase, c.recEnt = base, idx
	var err2 error
	if sc.flags&flagModel != 0 {
		err2 = c.indexMessages(&c.msgs, sc.model, base+uint32(sc.modelOff))
	}
	if err2 == nil && sc.flags&flagEdits != 0 {
		err2 = c.indexEdits(idx, sc.edits, base+uint32(sc.editsOff))
	}
	if err2 != nil {
		c.arena, c.chunkEnts = c.arena[:chunksMark], c.chunkEnts[:chunksMark]
		if chunksMark > 0 {
			c.arena[chunksMark-1] = c.arena[chunksMark-1][:lastMark]
		}
		c.msgs, c.emsgs, c.spans, c.edits, c.ids = c.msgs[:msgMark], c.emsgs[:emsgMark], c.spans[:spanMark], c.edits[:editMark], c.ids[:idMark]
		return err2
	}
	if sc.flags&flagHead != 0 {
		c.heads = append(c.heads, headRec{ent: idx, head: sc.head})
	}
	c.ents = push(c.ents, ent)
	if c.st != nil {
		c.st.resident += len(rec)
	}
	return nil
}

// indexMessages indexes the messages of a model array whose text starts at rec offset base (arena offset of the '[').
func (c *Chain) indexMessages(dst *[]msgRef, arr []byte, base uint32) error {
	s := newScanner(arr)
	it, err := s.array()
	if err != nil {
		return err
	}
	for it.next() {
		m, err := c.indexMessage(arr[it.vs:it.ve], base+uint32(it.vs))
		if err != nil {
			return err
		}
		*dst = push(*dst, m)
	}
	return it.err
}

// indexMessage scans one message object that starts at arena offset base.
func (c *Chain) indexMessage(msg []byte, base uint32) (msgRef, error) {
	ref := msgRef{off: base - c.recBase, ent: c.recEnt, len: uint32(len(msg)), calls: uint32(len(c.spans))}
	s := newScanner(msg)
	it, err := s.object()
	if err != nil {
		return ref, nil // not an object: an opaque message of role other
	}
	var content []byte
	var contentOff uint32
	var resultID span
	resultID = span{off: missingSpan}
	for it.next() {
		switch {
		case it.keyIs("role"):
			ref.role = roleOther
			if v := it.value(); valueKind(v) == '"' {
				ref.role = roleCode(v[1 : len(v)-1])
			}
		case it.keyIs("stopReason"):
			ref.stop = stopOther
			if v := it.value(); valueKind(v) == '"' {
				ref.stop = stopCode(v[1 : len(v)-1])
			}
		case it.keyIs("content"):
			content, contentOff = nil, 0
			if v := it.value(); valueKind(v) == '[' {
				content, contentOff = v, uint32(it.vs)
			}
		case it.keyIs("toolCallId"):
			resultID = span{off: missingSpan}
			if v := it.value(); valueKind(v) == '"' {
				resultID = c.idSpan(v[1 : len(v)-1])
			}
		}
	}
	if it.err != nil {
		return ref, it.err
	}
	switch ref.role {
	case roleToolResult:
		ref.nCalls = 1
		c.spans = push(c.spans, resultID)
	case roleAssistant:
		if content != nil {
			if err := c.indexCalls(&ref, content, base+contentOff); err != nil {
				return ref, err
			}
		}
	}
	return ref, nil
}

// idSpan is the span of a tool call id whose raw (still escaped) text is raw.
func (c *Chain) idSpan(raw []byte) span {
	at := uint32(len(c.ids))
	c.ids = append(c.ids, raw...)
	return span{off: at, len: uint32(len(raw)), hash: hashID(raw)}
}

// hashID is the FNV-1a hash of the string raw decodes to, the string sameID compares.
func hashID(raw []byte) uint32 {
	var buf [64]byte
	if hasEscape(raw) {
		raw = unescape(buf[:0], raw)
	}
	h := uint32(2166136261)
	for _, b := range raw {
		h = (h ^ uint32(b)) * 16777619
	}
	return h
}

// indexCalls records the id span of each tool call block of an assistant message's content array.
func (c *Chain) indexCalls(ref *msgRef, content []byte, base uint32) error {
	s := newScanner(content)
	ait, err := s.array()
	if err != nil {
		return err
	}
	for ait.next() {
		blk := content[ait.vs:ait.ve]
		if valueKind(blk) != '{' {
			continue
		}
		bs := newScanner(blk)
		oit, err := bs.object()
		if err != nil {
			return err
		}
		isCall := false
		id := span{off: missingSpan}
		for oit.next() {
			switch {
			case oit.keyIs("type"):
				isCall = false
				if v := oit.value(); valueKind(v) == '"' {
					isCall = keyIs(v[1:len(v)-1], "toolCall")
				}
			case oit.keyIs("id"):
				id = span{off: missingSpan}
				if v := oit.value(); valueKind(v) == '"' {
					id = c.idSpan(v[1 : len(v)-1])
				}
			}
		}
		if oit.err != nil {
			return oit.err
		}
		if isCall {
			c.spans = push(c.spans, id)
			ref.nCalls++
		}
	}
	return ait.err
}

func roleCode(raw []byte) uint8 {
	switch {
	case keyIs(raw, "user"):
		return roleUser
	case keyIs(raw, "assistant"):
		return roleAssistant
	case keyIs(raw, "toolResult"):
		return roleToolResult
	case keyIs(raw, "system"):
		return roleSystem
	}
	return roleOther
}

func stopCode(raw []byte) uint8 {
	switch {
	case keyIs(raw, "aborted"):
		return stopAborted
	case keyIs(raw, "error"):
		return stopError
	case keyIs(raw, "deferred"):
		return stopDeferred
	}
	return stopOther
}

// indexEdits indexes the edits array of entry idx; arr starts at arena offset base.
func (c *Chain) indexEdits(idx int32, arr []byte, base uint32) error {
	s := newScanner(arr)
	it, err := s.array()
	if err != nil {
		return err
	}
	for it.next() {
		raw := arr[it.vs:it.ve]
		if valueKind(raw) != '{' {
			continue
		}
		es := newScanner(raw)
		oit, err := es.object()
		if err != nil {
			return err
		}
		rec := editRec{ent: idx}
		var msgs []byte
		var msgsOff uint32
		hasTarget := false
		for oit.next() {
			switch {
			case oit.keyIs("target"):
				rec.target, hasTarget = intValue(oit.value())
			case oit.keyIs("action"):
				rec.omit, rec.replace = false, false
				if v := oit.value(); valueKind(v) == '"' {
					rec.omit = keyIs(v[1:len(v)-1], "omit")
					rec.replace = keyIs(v[1:len(v)-1], "replace")
				}
			case oit.keyIs("messages"):
				msgs, msgsOff = nil, 0
				if v := oit.value(); valueKind(v) == '[' {
					msgs, msgsOff = v, uint32(oit.vs)
				}
			}
		}
		if oit.err != nil {
			return oit.err
		}
		if !hasTarget {
			continue
		}
		rec.msg0 = uint32(len(c.emsgs))
		if rec.replace && msgs != nil {
			if err := c.indexMessages(&c.emsgs, msgs, base+uint32(it.vs)+msgsOff); err != nil {
				return err
			}
			rec.nMsg = uint32(len(c.emsgs)) - rec.msg0
		}
		c.edits = append(c.edits, rec)
	}
	return it.err
}

// editsOf returns the edits of the entry at position idx, in array order. cursor is the position in c.edits to
// start from; callers that walk entries in order pass the previous result.
func (c *Chain) editsOf(idx int32, cursor int) (from, to int) {
	for cursor < len(c.edits) && c.edits[cursor].ent < idx {
		cursor++
	}
	to = cursor
	for to < len(c.edits) && c.edits[to].ent == idx {
		to++
	}
	return cursor, to
}
