// SPDX-License-Identifier: MIT

package history

import (
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

// span is a byte range of a chain's arena.
type span struct{ off, len uint32 }

// entry is one fixed-width index record of a chain (ADR-0001 D4).
type entry struct {
	id    int64
	seq   int64  // commit_seq of the commit that wrote the entry
	off   uint32 // start of the record in the arena; it ends where the next entry's record starts
	msg0  uint32 // first model message in Chain.msgs; they end where the next entry's start
	kind  uint16
	flags uint8
}

// msgRef indexes one message of a record: its bytes, role, stop reason and tool call ids.
type msgRef struct {
	off, len uint32
	// nCalls is the number of tool calls of an assistant message, or 1 for a tool result's call id.
	nCalls uint32
	// calls indexes Chain.spans: nCalls spans of call ids (assistant) or one span (tool result).
	calls uint32
	role  uint8
	stop  uint8
}

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

	arena []byte
	ents  []entry
	msgs  []msgRef // the model messages of the entries, in entry order
	emsgs []msgRef // the replacement messages of edits, in entry order
	spans []span
	heads []headRec
	edits []editRec
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

// ArenaBytes is the size of the record arena.
func (c *Chain) ArenaBytes() int { return len(c.arena) }

// IndexBytes approximates the memory the fixed-width index uses.
func (c *Chain) IndexBytes() int {
	return len(c.ents)*32 + (len(c.msgs)+len(c.emsgs))*20 + len(c.spans)*8 + len(c.heads)*16 + len(c.edits)*32
}

func (c *Chain) reset(from, to int64) {
	c.From, c.To = from, to
	c.arena = c.arena[:0]
	c.ents = c.ents[:0]
	c.msgs = c.msgs[:0]
	c.emsgs = c.emsgs[:0]
	c.spans = c.spans[:0]
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

// bytes returns the stored record of entry i. It stays valid until the next append to the chain.
func (c *Chain) record(i int) []byte {
	end := uint32(len(c.arena))
	if i+1 < len(c.ents) {
		end = c.ents[i+1].off
	}
	return c.arena[c.ents[i].off:end]
}

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

func (c *Chain) msgBytes(m *msgRef) []byte { return c.arena[m.off : m.off+m.len] }

func (c *Chain) spanBytes(sp span) []byte { return c.arena[sp.off : sp.off+sp.len] }

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
		if string(known) == string(k) {
			return uint16(i + 1)
		}
	}
	if len(st.kinds) >= math.MaxUint16-1 {
		return 0
	}
	st.kinds = append(st.kinds, append([]byte(nil), k...))
	return uint16(len(st.kinds))
}

// KindName returns the kind string of a kind code; code 0 is the empty (absent) kind.
func (st *Store) kindName(code uint16) []byte {
	if code == 0 || int(code) > len(st.kinds) {
		return nil
	}
	return st.kinds[code-1]
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
	base := uint32(len(c.arena))
	if uint64(len(c.arena))+uint64(len(rec)) > math.MaxUint32 {
		return jsonError("arena exceeds 4 GiB")
	}
	arenaMark, msgMark, emsgMark, spanMark, editMark := len(c.arena), len(c.msgs), len(c.emsgs), len(c.spans), len(c.edits)
	c.arena = append(c.arena, rec...)
	ent := entry{id: id, seq: seq, off: base, msg0: uint32(len(c.msgs)), flags: sc.flags}
	ent.kind = st.internKind(sc.kind)
	idx := int32(len(c.ents))
	var err2 error
	if sc.flags&flagModel != 0 {
		err2 = c.indexMessages(&c.msgs, sc.model, base+uint32(sc.modelOff))
	}
	if err2 == nil && sc.flags&flagEdits != 0 {
		err2 = c.indexEdits(idx, sc.edits, base+uint32(sc.editsOff))
	}
	if err2 != nil {
		c.arena, c.msgs, c.emsgs, c.spans, c.edits = c.arena[:arenaMark], c.msgs[:msgMark], c.emsgs[:emsgMark], c.spans[:spanMark], c.edits[:editMark]
		return err2
	}
	if sc.flags&flagHead != 0 {
		c.heads = append(c.heads, headRec{ent: idx, head: sc.head})
	}
	c.ents = append(c.ents, ent)
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
		*dst = append(*dst, m)
	}
	return it.err
}

// indexMessage scans one message object that starts at arena offset base.
func (c *Chain) indexMessage(msg []byte, base uint32) (msgRef, error) {
	ref := msgRef{off: base, len: uint32(len(msg)), calls: uint32(len(c.spans))}
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
				resultID = span{off: base + uint32(it.vs) + 1, len: uint32(len(v) - 2)}
			}
		}
	}
	if it.err != nil {
		return ref, it.err
	}
	switch ref.role {
	case roleToolResult:
		ref.nCalls = 1
		c.spans = append(c.spans, resultID)
	case roleAssistant:
		if content != nil {
			if err := c.indexCalls(&ref, content, base+contentOff); err != nil {
				return ref, err
			}
		}
	}
	return ref, nil
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
					id = span{off: base + uint32(ait.vs) + uint32(oit.vs) + 1, len: uint32(len(v) - 2)}
				}
			}
		}
		if oit.err != nil {
			return oit.err
		}
		if isCall {
			c.spans = append(c.spans, id)
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
