// SPDX-License-Identifier: MIT

package history

import (
	"slices"
	"strconv"
)

// eref names an entry of a chain; mref names a message of a chain, or a message the view synthesized when ch is negative.
type eref struct{ ch, idx int32 }

type mref struct{ ch, idx int32 }

// Chain numbers of synthesized messages: those of the settled part and those of the open part.
const (
	synthSettled int32 = -1
	synthOpen    int32 = -2
)

// missingResultText is the content of a synthesized result (src/harness/context.ts:9).
const missingResultText = "Tool result unavailable: history ends before this call completed."

func (st *Store) ent(e eref) *entry { return &st.chainList[e.ch].ents[e.idx] }

func (st *Store) chainRef(e eref) *Chain { return st.chainList[e.ch] }

// derived is one derivation of a conversation's context: the range it scanned, the edits found in it, the active
// entries with their contributions, and the model messages. The messages before the last assistant message are
// settled: tool results are ordered within each stretch up to the next assistant message, so entries appended later
// cannot change them (src/harness/context.ts, ContextRange).
type derived struct {
	conv      int64
	tail      int64
	hasHead   bool
	headID    int64
	headEntry eref
	rng       []eref
	edits     map[int64]editRef
	active    []eref
	cOff      []int32 // contributions of active[i] are contrib[cOff[i]:cOff[i+1]]
	contrib   []mref
	settled   []mref   // ordered messages before open
	open      []mref   // contributed messages from the last assistant message on, before tool result ordering
	openFrom  int      // index in contrib where open starts
	synth     [][]byte // messages synthesized into settled (mref.ch == -1)
	openSynth [][]byte // messages synthesized into the open part (mref.ch == -2)
	out       []mref   // settled followed by the ordered open messages, system message led
}

type editRef struct {
	ch, idx int32
}

// View is a derived context. It aliases store memory: it stays valid until the next call that changes the store or
// asks for another context of the same conversation.
type View struct {
	st *Store
	d  *derived
}

// Tail is the id of the newest entry of the range, or 0 for an empty context.
func (v *View) Tail() int64 { return v.d.tail }

// HasHead reports whether a head marker bounds the range.
func (v *View) HasHead() bool { return v.d.hasHead }

// HeadID is the head marker's entry id.
func (v *View) HeadID() int64 { return v.d.headID }

// NumEntries is the number of active entries: the head marker, then the range's non-head entries.
func (v *View) NumEntries() int { return len(v.d.active) }

// EntryID is the id of active entry i.
func (v *View) EntryID(i int) int64 { return v.st.ent(v.d.active[i]).id }

// EntryRecord returns the stored bytes of active entry i.
func (v *View) EntryRecord(i int) []byte {
	e := v.d.active[i]
	return v.st.chainRef(e).record(int(e.idx))
}

// EntryKind returns the kind of active entry i.
func (v *View) EntryKind(i int) []byte { return v.st.kindName(v.st.ent(v.d.active[i]).kind) }

// EntrySeq is the commit sequence of active entry i's commit.
func (v *View) EntrySeq(i int) int64 { return v.st.ent(v.d.active[i]).seq }

// NumContributions is the number of messages active entry i contributes after edits and excluded stop reasons.
func (v *View) NumContributions(i int) int { return int(v.d.cOff[i+1] - v.d.cOff[i]) }

// Contribution returns message j of active entry i's contribution.
func (v *View) Contribution(i, j int) []byte {
	return v.st.msgBytes(v.d, v.d.contrib[int(v.d.cOff[i])+j])
}

// NumMessages is the number of model messages.
func (v *View) NumMessages() int { return len(v.d.out) }

// Message returns model message i (JSON bytes).
func (v *View) Message(i int) []byte { return v.st.msgBytes(v.d, v.d.out[i]) }

func (st *Store) msgBytes(d *derived, m mref) []byte {
	switch m.ch {
	case synthSettled:
		return d.synth[m.idx]
	case synthOpen:
		return d.openSynth[m.idx]
	}
	c := st.chainList[m.ch]
	return c.msgBytes(c.msgAt(m.idx))
}

func (st *Store) msgInfo(m mref) *msgRef {
	if m.ch < 0 {
		return nil
	}
	c := st.chainList[m.ch]
	return c.msgAt(m.idx)
}

// role returns the role code of a message; a synthesized message is a tool result.
func (st *Store) role(m mref) uint8 {
	if r := st.msgInfo(m); r != nil {
		return r.role
	}
	return roleToolResult
}

// callIDs returns the call id spans of assistant message m, or the single id of a tool result.
func (st *Store) callSpans(m mref) []span {
	c := st.chainList[m.ch]
	r := c.msgAt(m.idx)
	return c.spans[r.calls : r.calls+r.nCalls]
}

// sameID compares two call ids as JavaScript strings. A missing id equals only a missing id.
func (st *Store) sameID(a mref, sa span, b mref, sb span) bool {
	if sa.off == missingSpan || sb.off == missingSpan {
		return sa.off == missingSpan && sb.off == missingSpan
	}
	ra := st.chainList[a.ch].spanBytes(sa)
	rb := st.chainList[b.ch].spanBytes(sb)
	if !hasEscape(ra) && !hasEscape(rb) {
		return string(ra) == string(rb)
	}
	var x, y [64]byte
	return string(unescape(x[:0], ra)) == string(unescape(y[:0], rb))
}

// excluded reports whether an assistant message is left out of model context (spec section 2.1, rule 9).
func excluded(r *msgRef) bool {
	return r.role == roleAssistant && (r.stop == stopAborted || r.stop == stopError || r.stop == stopDeferred)
}

// contribute appends active entry e's messages after its edit and excluded stop reasons.
func (st *Store) contribute(dst []mref, e eref, edit *editRef) []mref {
	c := st.chainRef(e)
	if edit != nil {
		ec := st.chainList[edit.ch]
		er := &ec.edits[edit.idx]
		if er.omit {
			return dst
		}
		if er.replace {
			for k := uint32(0); k < er.nMsg; k++ {
				if idx := ^int32(er.msg0 + k); !excluded(ec.msgAt(idx)) {
					dst = append(dst, mref{edit.ch, idx})
				}
			}
			return dst
		}
	}
	from, to := c.modelMessages(int(e.idx))
	for k := from; k < to; k++ {
		if !excluded(&c.msgs[k]) {
			dst = append(dst, mref{e.ch, int32(k)})
		}
	}
	return dst
}

// scanEdits records the newest edit per target among the edits of the entries in refs (the newest wins; within an
// entry the later edit wins).
func (st *Store) scanEdits(m map[int64]editRef, refs []eref) {
	for _, e := range refs {
		c := st.chainRef(e)
		if c.ents[e.idx].flags&flagEdits == 0 {
			continue
		}
		from, to := c.editsOf(e.idx, 0)
		for k := from; k < to; k++ {
			m[c.edits[k].target] = editRef{e.ch, int32(k)}
		}
	}
}

func hasHeadFlags(f uint8) bool { return f&(flagHead|flagHeadNull) != 0 }

// build derives the context of range refs under head marker head (hasHead false: none).
func (st *Store) build(conv, tail int64, hasHead bool, headRef eref, refs []eref) *derived {
	d := &derived{conv: conv, tail: tail, hasHead: hasHead, headEntry: headRef, rng: refs, edits: map[int64]editRef{}}
	if hasHead {
		d.headID = st.ent(headRef).id
	}
	st.scanEdits(d.edits, refs)
	d.active = d.active[:0]
	if hasHead {
		d.active = append(d.active, headRef)
	}
	for _, e := range refs {
		if hasHead && hasHeadFlags(st.ent(e).flags) {
			continue
		}
		d.active = append(d.active, e)
	}
	d.cOff = make([]int32, 0, len(d.active)+1)
	for _, e := range d.active {
		d.cOff = append(d.cOff, int32(len(d.contrib)))
		var ed *editRef
		if r, ok := d.edits[st.ent(e).id]; ok {
			ed = &r
		}
		d.contrib = st.contribute(d.contrib, e, ed)
	}
	d.cOff = append(d.cOff, int32(len(d.contrib)))
	d.openFrom = 0
	st.settle(d, 0)
	return d
}

// settle moves the messages of contrib[from-of-open:] before the last assistant message into settled, ordered.
// d.open must hold contrib[d.openFrom:]; the caller keeps it so.
func (st *Store) settle(d *derived, _ int) {
	d.open = append(d.open[:0], d.contrib[d.openFrom:]...)
	last := -1
	for i, v := range slices.Backward(d.open) {
		if st.role(v) == roleAssistant {
			last = i
			break
		}
	}
	if last > 0 {
		d.settled = st.orderToolResults(d, d.settled, d.open[:last], synthSettled)
		d.openFrom += last
		d.open = append(d.open[:0], d.contrib[d.openFrom:]...)
	}
	st.assemble(d)
}

// assemble builds the final message list: settled, then the ordered open messages, led by a system message that
// only user messages precede.
func (st *Store) assemble(d *derived) {
	d.out = append(d.out[:0], d.settled...)
	d.openSynth = d.openSynth[:0]
	d.out = st.orderToolResults(d, d.out, d.open, synthOpen)
	d.out = st.leadWithSystem(d.out)
}

// leadWithSystem moves a system message that only user messages precede to the front (spec section 2.1, rule 10).
func (st *Store) leadWithSystem(m []mref) []mref {
	idx := -1
	for i, x := range m {
		if st.role(x) != roleUser {
			idx = i
			break
		}
	}
	if idx <= 0 || st.role(m[idx]) != roleSystem {
		return m
	}
	sys := m[idx]
	copy(m[1:idx+1], m[:idx])
	m[0] = sys
	return m
}

// orderToolResults appends to dst the messages placed so each assistant message's tool results directly follow it in
// call order: results are taken from the messages before the next assistant message, a missing result is
// synthesized and an unmatched result is dropped (src/harness/context.ts orderToolResults, spec rules 7 and 8).
func (st *Store) orderToolResults(d *derived, dst []mref, msgs []mref, synth int32) []mref {
	for i := range msgs {
		m := msgs[i]
		role := st.role(m)
		if role == roleToolResult {
			continue
		}
		dst = append(dst, m)
		if role != roleAssistant {
			continue
		}
		calls := st.callSpans(m)
		if len(calls) == 0 {
			continue
		}
		for k, call := range calls {
			found := -1
			for j := i + 1; j < len(msgs) && st.role(msgs[j]) != roleAssistant; j++ {
				if st.role(msgs[j]) == roleToolResult {
					if st.sameID(m, call, msgs[j], st.callSpans(msgs[j])[0]) {
						found = j
						break
					}
				}
			}
			if found >= 0 {
				dst = append(dst, msgs[found])
			} else {
				dst = append(dst, st.synthesize(d, m, k, synth))
			}
		}
	}
	return dst
}

// synthesize builds the error result of call k of assistant message m.
func (st *Store) synthesize(d *derived, m mref, k int, synth int32) mref {
	c := st.chainList[m.ch]
	raw := c.msgBytes(c.msgAt(m.idx))
	var id, name, ts []byte
	hasID, hasName, hasTS := false, false, false
	s := newScanner(raw)
	if it, err := s.object(); err == nil {
		for it.next() {
			switch {
			case it.keyIs("timestamp"):
				ts, hasTS = nil, false
				if v := it.value(); valueKind(v) == 'n' {
					ts, hasTS = v, true
				}
			case it.keyIs("content"):
				v := it.value()
				if valueKind(v) != '[' {
					continue
				}
				cs := newScanner(v)
				ait, err := cs.array()
				if err != nil {
					continue
				}
				n := 0
				for ait.next() {
					blk := v[ait.vs:ait.ve]
					if valueKind(blk) != '{' {
						continue
					}
					bs := newScanner(blk)
					oit, err := bs.object()
					if err != nil {
						continue
					}
					isCall := false
					var bid, bname []byte
					var hid, hname bool
					for oit.next() {
						switch {
						case oit.keyIs("type"):
							isCall = false
							if x := oit.value(); valueKind(x) == '"' {
								isCall = keyIs(x[1:len(x)-1], "toolCall")
							}
						case oit.keyIs("id"):
							bid, hid = nil, false
							if x := oit.value(); valueKind(x) == '"' {
								bid, hid = x[1:len(x)-1], true
							}
						case oit.keyIs("name"):
							bname, hname = nil, false
							if x := oit.value(); valueKind(x) == '"' {
								bname, hname = x[1:len(x)-1], true
							}
						}
					}
					if !isCall {
						continue
					}
					if n == k {
						id, name, hasID, hasName = bid, bname, hid, hname
					}
					n++
				}
			}
		}
	}
	var b []byte
	b = append(b, `{"role":"toolResult"`...)
	if hasID {
		b = append(b, `,"toolCallId":`...)
		b = appendRawString(b, id)
	}
	if hasName {
		b = append(b, `,"toolName":`...)
		b = appendRawString(b, name)
	}
	b = append(b, `,"content":[{"type":"text","text":`...)
	b = appendString(b, []byte(missingResultText))
	b = append(b, `}],"isError":true,"details":{"reason":"missing_result"}`...)
	if hasTS {
		b = append(b, `,"timestamp":`...)
		b = appendNumberText(b, ts)
	}
	b = append(b, '}')
	if synth == synthSettled {
		d.synth = append(d.synth, b)
		return mref{synthSettled, int32(len(d.synth) - 1)}
	}
	d.openSynth = append(d.openSynth, b)
	return mref{synthOpen, int32(len(d.openSynth) - 1)}
}

// emptyDerived is the context of a conversation without entries.
func emptyDerived(conv int64) *derived {
	return &derived{conv: conv, cOff: []int32{0}}
}

// extend appends entries added after d's tail under the same head marker. It reports false when an added entry
// carries edits or a head, or targets an edited entry: the whole range must then be derived again.
func (st *Store) extend(d *derived, tail int64, added []eref) bool {
	for _, e := range added {
		en := st.ent(e)
		if en.flags&flagEdits != 0 || hasHeadFlags(en.flags) {
			return false
		}
		if _, ok := d.edits[en.id]; ok {
			return false
		}
	}
	d.rng = append(d.rng, added...)
	for _, e := range added {
		d.active = append(d.active, e)
		d.contrib = st.contribute(d.contrib, e, nil)
		d.cOff = append(d.cOff, int32(len(d.contrib)))
	}
	d.tail = tail
	st.settle(d, 0)
	return true
}

func appendInt(dst []byte, v int64) []byte { return strconv.AppendInt(dst, v, 10) }
