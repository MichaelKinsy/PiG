// SPDX-License-Identifier: MIT

package history

import "slices"

// NotVisibleError reports a context request cut at an entry the conversation cannot see
// (`Entry ${at} is not visible from conversation ${conversationId}`, src/harness/context.ts).
type NotVisibleError struct{ Entry, Conv int64 }

func (e *NotVisibleError) Error() string {
	return "Entry " + itoa(e.Entry) + " is not visible from conversation " + itoa(e.Conv)
}

func itoa(v int64) string { return string(appendInt(nil, v)) }

// collect gathers the entries of the segments with ids in [lo, hi] (lo 0 means from the start), oldest first, and
// asks for any segment whose chain does not cover the range.
func (st *Store) collect(conv, lo, hi int64, tip bool) ([]eref, *Need) {
	var segs [8]segment
	list := segs[:0]
	cur, upper := conv, hi
	for {
		cs := st.convs[cur]
		if cs == nil || !cs.known {
			return nil, &Need{Kind: NeedConversation, Conv: cur}
		}
		list = append(list, segment{cur, upper})
		if !cs.rec.HasParent {
			break
		}
		if cs.rec.ParentAt < upper {
			upper = cs.rec.ParentAt
		}
		if lo > 0 && upper < lo {
			break
		}
		cur = cs.rec.ParentConv
	}
	for k, sg := range list {
		ch := st.chainOf(sg.conv, true)
		if ch.From == unloadedFrom || (ch.From != 0 && ch.From > lo) || ch.To < sg.upper {
			max := sg.upper
			if k == 0 && tip {
				max = unbounded
			}
			return nil, &Need{Kind: NeedEntries, Conv: sg.conv, Min: lo, Max: max}
		}
	}
	var out []eref
	for _, sg := range slices.Backward(list) {

		ch := st.chainOf(sg.conv, true)
		no := st.chainNo[sg.conv]
		i := 0
		if lo > 0 {
			i = ch.search(lo)
		}
		for ; i < len(ch.ents) && ch.ents[i].id <= sg.upper; i++ {
			out = append(out, eref{no, int32(i)})
		}
	}
	return out, nil
}

// bounds is the result of finding a context's cutoff: its newest entry (tail) and its newest head marker.
type bounds struct {
	found      bool // the conversation has a visible entry (at the cutoff)
	tail       int64
	hasMarker  bool
	markerConv int64
	markerID   int64
	markerHead int64
}

// findBounds walks the fork chain from conv, child first. at is the cutoff entry, or 0 for the newest visible entry
// (`captureContextBounds`, src/harness/context.ts:22-39, and findLatestHeadMarker).
func (st *Store) findBounds(conv, at int64) (b bounds, need *Need, err error) {
	upper := unbounded
	if at != 0 {
		upper = at
	}
	cur := conv
	for {
		cs := st.convs[cur]
		if cs == nil || !cs.known {
			return b, &Need{Kind: NeedConversation, Conv: cur}, nil
		}
		h, n := st.segBounds(segment{cur, upper})
		if n != nil {
			return b, n, nil
		}
		if !b.found && h.hasMax && (at == 0 || h.maxID == at) {
			b.found, b.tail = true, h.maxID
		}
		if b.found && h.hasMarker {
			b.hasMarker, b.markerConv, b.markerID, b.markerHead = true, cur, h.markerID, h.markerHead
			return b, nil, nil
		}
		if !cs.rec.HasParent {
			break
		}
		if b.found && b.tail < upper {
			upper = b.tail
		}
		if cs.rec.ParentAt < upper {
			upper = cs.rec.ParentAt
		}
		cur = cs.rec.ParentConv
	}
	if at != 0 && !b.found {
		return b, nil, &NotVisibleError{Entry: at, Conv: conv}
	}
	return b, nil, nil
}

// Context derives the model context of a conversation: the newest one when at is 0, else the context cut off at
// entry at. It returns a Need when it must read rows first; the host answers it and calls Context again. The
// newest context of a conversation is cached and extended by entries appended after it.
func (st *Store) Context(conv, at int64) (*View, *Need, error) {
	b, need, err := st.findBounds(conv, at)
	if err != nil || need != nil {
		return nil, need, err
	}
	if !b.found {
		return &View{st: st, d: emptyDerived(conv)}, nil, nil
	}
	cs := st.convs[conv]
	lo := int64(0)
	if b.hasMarker {
		lo = b.markerHead
	}
	// The cached newest context extends when the marker is the same and the tail only grew.
	if d := cs.ctx; at == 0 && d != nil && d.hasHead == b.hasMarker && (!b.hasMarker || d.headID == b.markerID) && d.tail <= b.tail {
		if d.tail == b.tail {
			return &View{st: st, d: d}, nil, nil
		}
		added, need := st.collect(conv, d.tail+1, b.tail, true)
		if need != nil {
			return nil, need, nil
		}
		if st.extend(d, b.tail, added) {
			st.extended++
			return &View{st: st, d: d}, nil, nil
		}
	}
	refs, need := st.collect(conv, lo, b.tail, at == 0)
	if need != nil {
		return nil, need, nil
	}
	var headRef eref
	if b.hasMarker {
		no, ok := st.chainNo[b.markerConv]
		if !ok {
			return nil, &Need{Kind: NeedEntries, Conv: b.markerConv, Min: lo, Max: b.markerID}, nil
		}
		i := st.chainList[no].find(b.markerID)
		if i < 0 {
			return nil, &Need{Kind: NeedEntries, Conv: b.markerConv, Min: lo, Max: b.markerID}, nil
		}
		headRef = eref{no, int32(i)}
	}
	st.rebuilt++
	d := st.build(conv, b.tail, b.hasMarker, headRef, refs)
	if at == 0 {
		cs.ctx = d
	}
	return &View{st: st, d: d}, nil, nil
}
