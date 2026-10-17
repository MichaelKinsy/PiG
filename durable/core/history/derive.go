// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
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
	conv       int64
	tail       int64
	hasHead    bool
	headID     int64
	headEntry  eref
	edits      map[int64]editRef
	active     []eref
	cOff       []int32 // contributions of active[i] are contrib[cOff[i]:cOff[i+1]]
	contrib    []mref
	sys        []mref   // the system messages of contrib, in order
	settled    []mref   // ordered messages before open
	open       []mref   // contributed messages from the last assistant message on, before tool result ordering
	openFrom   int      // index in contrib where open starts
	synth      [][]byte // messages synthesized into settled (mref.ch == -1)
	openSynth  [][]byte // messages synthesized into the open part (mref.ch == -2)
	userPrefix int      // the number of user messages settled starts with
	settledOut []mref   // the model messages' settled part, system message led: shared with the derivation it extended
	lineage    uint64   // names settledOut's contents: a derivation that extends this one keeps it and only appends (the lead copy in assemble happens while settledOut is still empty)
	// baseResolved is set once the baseline is known (findBaseline in harness/context.go, Pi 5713c950): baseID is the entry
	// whose system message is the baseline, 0 when the range has none. firstSysEnt is the first active entry contributing a
	// system message, 0 when none does. leadDone is set once the first system message is in settled, where the decision to lead
	// with it was made; scanned is how many settled messages were searched for it.
	baseResolved bool
	baseID       int64
	firstSysEnt  int64
	leadDone     bool
	scanned      int
	openOut      []mref // the rest: the ordered open messages, or the whole list when settled holds only user messages
	// reads are the chains the context reads and the epoch each had when it was derived: a chain that was reset since
	// (a bounded chain Append cannot extend, a reload, a release) no longer holds the entries the context's references name.
	reads []chainEpoch
}

type chainEpoch struct {
	ch    int32
	epoch uint32
}

// noteRead records that d reads chain ch.
func (st *Store) noteRead(d *derived, ch int32) {
	for _, r := range d.reads {
		if r.ch == ch {
			return
		}
	}
	d.reads = append(d.reads, chainEpoch{ch, st.chainList[ch].epoch})
}

// current reports whether every chain d reads is unchanged since d was derived.
func (st *Store) current(d *derived) bool {
	for _, r := range d.reads {
		if st.chainList[r.ch].epoch != r.epoch {
			return false
		}
	}
	return true
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

// Valid reports whether every chain the view reads is unchanged since it was derived. A reset chain (Release, a new load,
// an append the chain cannot extend) leaves the view's entry references dangling: derive the context again.
func (v *View) Valid() bool { return v.st.current(v.d) }

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

// EntryKindString is EntryKind as a string, shared by every entry of the kind: reading the kinds of a whole context
// allocates nothing.
func (v *View) EntryKindString(i int) string { return v.st.kindString(v.st.ent(v.d.active[i]).kind) }

// EntrySeq is the commit sequence of active entry i's commit.
func (v *View) EntrySeq(i int) int64 { return v.st.ent(v.d.active[i]).seq }

// NumContributions is the number of messages active entry i contributes after edits and excluded stop reasons.
func (v *View) NumContributions(i int) int { return int(v.d.cOff[i+1] - v.d.cOff[i]) }

// Contribution returns message j of active entry i's contribution.
func (v *View) Contribution(i, j int) []byte {
	return v.st.msgBytes(v.d, v.d.contrib[int(v.d.cOff[i])+j])
}

// NumMessages is the number of model messages.
func (v *View) NumMessages() int { return v.d.numOut() }

// NumSystemMessages is the number of model messages with the role "system", found while the context was derived.
func (v *View) NumSystemMessages() int { return len(v.d.sys) }

// SystemMessage returns system message j (JSON bytes), in the order of the model messages.
func (v *View) SystemMessage(j int) []byte { return v.st.msgBytes(v.d, v.d.sys[j]) }

// MessageIdent names the stored bytes of a model message: the chain, the message's index in it and the chain's epoch. The
// bytes behind an identity never change, so two views whose messages carry the same identity carry the same message. A
// synthesized message (a tool result the context supplies for an unanswered call) has no identity and reports false.
type MessageIdent struct {
	chain, index int32
	epoch        uint32
}

// MessageIdent returns the identity of model message i; false for a synthesized message.
func (v *View) MessageIdent(i int) (MessageIdent, bool) {
	m := v.d.outAt(i)
	if m.ch < 0 {
		return MessageIdent{}, false
	}
	return MessageIdent{m.ch, m.idx, v.st.chainList[m.ch].epoch}, true
}

// Settled returns the lineage of the view's settled messages and their number: the leading model messages that messages
// appended later cannot reorder. Two views of a store whose lineages are equal and not zero agree on every settled message
// both have (the same bytes and the same identity), so a reader that kept an earlier view's messages need not compare that
// prefix again.
func (v *View) Settled() (lineage uint64, n int) { return v.d.lineage, len(v.d.settledOut) }

// Message returns model message i (JSON bytes).
func (v *View) Message(i int) []byte { return v.st.msgBytes(v.d, v.d.outAt(i)) }

// numOut is the number of model messages: the settled head and the tail after it.
func (d *derived) numOut() int { return len(d.settledOut) + len(d.openOut) }

// outAt is model message i.
func (d *derived) outAt(i int) mref {
	if i < len(d.settledOut) {
		return d.settledOut[i]
	}
	return d.openOut[i-len(d.settledOut)]
}

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

// Bits of measure.known.
const (
	knownTokens uint8 = 1 << iota
	knownUsage
)

// measureOf is the measure of an indexed message of a chain, allocating its block on first use; nil for a message that is
// synthesized or replaces an edited one (those are measured each time).
func (st *Store) measureOf(m mref) *measure {
	if m.ch < 0 || m.idx < 0 {
		return nil
	}
	c := st.chainList[m.ch]
	b := int(m.idx) / measureBlock
	for len(c.meas) <= b {
		c.meas = append(c.meas, nil)
	}
	if c.meas[b] == nil {
		c.meas[b] = make([]measure, measureBlock)
	}
	return &c.meas[b][int(m.idx)%measureBlock]
}

// tokens is EstimateMessageTokens of message m. An indexed message is read once and the answer kept.
func (st *Store) tokens(d *derived, m mref) float64 {
	r := st.measureOf(m)
	if r == nil {
		return EstimateMessageTokens(st.msgBytes(d, m))
	}
	if r.known&knownTokens == 0 {
		r.tokens = uint32(EstimateMessageTokens(st.msgBytes(d, m)))
		r.known |= knownTokens
	}
	return float64(r.tokens)
}

// usage is CalculateContextTokens of message m, kept like tokens.
func (st *Store) usage(d *derived, m mref) float64 {
	r := st.measureOf(m)
	if r == nil {
		return CalculateContextTokens(st.msgBytes(d, m))
	}
	if r.known&knownUsage == 0 {
		r.usage = CalculateContextTokens(st.msgBytes(d, m))
		r.known |= knownUsage
	}
	return r.usage
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
	if sa.hash != sb.hash {
		return false
	}
	ra := st.chainList[a.ch].spanBytes(sa)
	rb := st.chainList[b.ch].spanBytes(sb)
	if !hasEscape(ra) && !hasEscape(rb) {
		return bytes.Equal(ra, rb)
	}
	var x, y [64]byte
	return bytes.Equal(unescape(x[:0], ra), unescape(y[:0], rb))
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
					dst = push(dst, mref{edit.ch, idx})
				}
			}
			return dst
		}
	}
	from, to := c.modelMessages(int(e.idx))
	for k := from; k < to; k++ {
		if !excluded(&c.msgs[k]) {
			dst = push(dst, mref{e.ch, int32(k)})
		}
	}
	return dst
}

// noteSystem appends the system messages of added to dst. The system messages of a context keep their contribution order
// in the final list: tool result ordering moves only tool results, and the system message led to the front is the first
// one.
func (st *Store) noteSystem(dst, added []mref) []mref {
	for _, m := range added {
		if st.role(m) == roleSystem {
			dst = append(dst, m)
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
	st.lineages++
	d := &derived{conv: conv, tail: tail, hasHead: hasHead, headEntry: headRef, edits: map[int64]editRef{}, lineage: st.lineages}
	if hasHead {
		d.headID = st.ent(headRef).id
	}
	st.scanEdits(d.edits, refs)
	if no, ok := st.chainNo[conv]; ok {
		st.noteRead(d, no)
	}
	if hasHead {
		st.noteRead(d, headRef.ch)
	}
	for i, e := range refs {
		if i == 0 || e.ch != refs[i-1].ch {
			st.noteRead(d, e.ch)
		}
	}
	for _, e := range d.edits {
		st.noteRead(d, e.ch)
	}
	d.active = make([]eref, 0, len(refs)+1)
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
	d.contrib = make([]mref, 0, len(d.active)+len(d.active)/8)
	for _, e := range d.active {
		d.cOff = append(d.cOff, int32(len(d.contrib)))
		var ed *editRef
		if r, ok := d.edits[st.ent(e).id]; ok {
			ed = &r
		}
		from := len(d.contrib)
		d.contrib = st.contribute(d.contrib, e, ed)
		st.noteBaseline(d, e)
		d.sys = st.noteSystem(d.sys, d.contrib[from:])
		if d.firstSysEnt == 0 && len(d.sys) > 0 {
			d.firstSysEnt = st.ent(e).id
		}
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
		d.settled = st.orderToolResults(d, room(d.settled, last), d.open[:last], synthSettled)
		d.openFrom += last
		d.open = append(d.open[:0], d.contrib[d.openFrom:]...)
	}
	for d.userPrefix < len(d.settled) && st.role(d.settled[d.userPrefix]) == roleUser {
		d.userPrefix++
	}
	if !d.leadDone {
		for ; d.scanned < len(d.settled); d.scanned++ {
			if st.role(d.settled[d.scanned]) == roleSystem {
				st.leadSettled(d, d.scanned)
				break
			}
		}
	}
	st.assemble(d)
}

// noteBaseline resolves the baseline from active entry e, in order (harness/context.go findBaseline): the first pi.system
// entry after the head marker, or of the transcript without one, whose system message is the baseline; without a head marker
// an earlier entry that carries a system message, even one an edit omits, leaves none.
func (st *Store) noteBaseline(d *derived, e eref) {
	if d.baseResolved {
		return
	}
	en := st.ent(e)
	if d.hasHead && en.id <= d.headID {
		return
	}
	c := st.chainRef(e)
	from, to := c.modelMessages(int(e.idx))
	own := false
	for k := from; k < to; k++ {
		if c.msgs[k].role == roleSystem {
			own = true
			break
		}
	}
	if st.kindString(en.kind) == "pi.system" {
		d.baseResolved = true
		if own {
			d.baseID = en.id
		}
		return
	}
	if d.hasHead {
		return
	}
	if !own && en.flags&flagEdits != 0 {
		ef, et := c.editsOf(e.idx, 0)
		for k := ef; k < et && !own; k++ {
			if !c.edits[k].replace {
				continue
			}
			for m := c.edits[k].msg0; m < c.edits[k].msg0+c.edits[k].nMsg; m++ {
				if c.emsgs[m].role == roleSystem {
					own = true
					break
				}
			}
		}
	}
	if own {
		d.baseResolved = true
	}
}

// leadsBaseline reports whether the first system message of the context is the baseline's own, unedited message.
func (d *derived) leadsBaseline() bool {
	if d.firstSysEnt == 0 || d.baseID != d.firstSysEnt {
		return false
	}
	_, edited := d.edits[d.firstSysEnt]
	return !edited
}

// leadSettled decides, when the first system message is found at settled[idx], whether it leads (Pi 5713c950): when only user
// messages precede it, or when it is the baseline's. The lead copies settled, which earlier views share, so it names new
// contents.
func (st *Store) leadSettled(d *derived, idx int) {
	d.leadDone = true
	if idx <= 0 || (d.userPrefix != idx && !d.leadsBaseline()) {
		return
	}
	led := append(make([]mref, 0, len(d.settled)+cap(d.settled)-len(d.settled)), d.settled[idx])
	led = append(led, d.settled[:idx]...)
	led = append(led, d.settled[idx+1:]...)
	d.settled = led
	d.userPrefix = 0
	if len(d.settledOut) > 0 {
		st.lineages++
		d.lineage = st.lineages
	}
}

// assemble builds the final message list: settled, then the ordered open messages, led by the first system message when only
// user messages precede it or it is the baseline's. The settled part is not copied per request: once it holds the first
// system message the lead decision is made inside it (leadSettled), led once into a copy (earlier views share the old array),
// and every later derivation shares it. Before that, a system message in the open part that leads makes the whole list be
// built, led and held as the open part.
func (st *Store) assemble(d *derived) {
	d.openSynth = d.openSynth[:0]
	ordered := st.orderToolResults(d, make([]mref, 0, len(d.open)), d.open, synthOpen)
	if !d.leadDone && len(d.sys) > 0 {
		at := -1
		for i, m := range ordered {
			if st.role(m) == roleSystem {
				at = i
				break
			}
		}
		onlyUsers := d.userPrefix == len(d.settled)
		for i := 0; at > 0 && onlyUsers && i < at; i++ {
			onlyUsers = st.role(ordered[i]) == roleUser
		}
		if at >= 0 && (d.leadsBaseline() || (onlyUsers && (len(d.settled) > 0 || at > 0))) {
			full := append(make([]mref, 0, len(d.settled)+len(ordered)), d.settled...)
			full = append(full, ordered...)
			sys := full[len(d.settled)+at]
			if idx := len(d.settled) + at; idx > 0 {
				copy(full[1:idx+1], full[:idx])
				full[0] = sys
			}
			// Settled messages an earlier view shared are held in the open part now and reordered: a new lineage.
			if len(d.settledOut) > 0 {
				st.lineages++
				d.lineage = st.lineages
			}
			d.settledOut, d.openOut = nil, full
			return
		}
	}
	if !d.leadDone && d.userPrefix == len(d.settled) {
		d.settledOut, d.openOut = nil, append(make([]mref, 0, len(d.settled)+len(ordered)), append(append([]mref(nil), d.settled...), ordered...)...)
		return
	}
	d.settledOut, d.openOut = d.settled, ordered
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
		dst = push(dst, m)
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
				dst = push(dst, msgs[found])
			} else {
				dst = push(dst, st.synthesize(d, m, k, synth))
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
	for _, e := range added {
		d.active = push(d.active, e)
		from := len(d.contrib)
		d.contrib = st.contribute(d.contrib, e, nil)
		st.noteBaseline(d, e)
		d.sys = st.noteSystem(d.sys, d.contrib[from:])
		if d.firstSysEnt == 0 && len(d.sys) > 0 {
			d.firstSysEnt = st.ent(e).id
		}
		d.cOff = push(d.cOff, int32(len(d.contrib)))
	}
	d.tail = tail
	st.settle(d, 0)
	return true
}

func appendInt(dst []byte, v int64) []byte { return strconv.AppendInt(dst, v, 10) }
