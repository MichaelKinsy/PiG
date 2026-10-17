// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
)

// MessageRange is the committed context a model_context effect names, as the host derived it: the model messages as JSON, in
// order, and what stored bytes each is, so a handler that decoded an earlier range of the conversation decodes only the
// messages that differ. A stored message's bytes are the history store's record text, shared, not copied: a store never rewrites
// bytes it handed out, so they stay valid and must not be modified.
//
// Msgs, Idents and Stored are read-only views of arrays the host extends at their end: two ranges with the same non-zero Lineage
// agree on every index both have, so a handler that kept the earlier one need not compare that prefix.
type MessageRange struct {
	Conv    int64
	Lineage uint64
	Msgs    [][]byte               // message i as JSON
	Idents  []history.MessageIdent // identity of each message; !Stored[i] for a synthesized one
	Stored  []bool
}

// sentRange is the message list of the last range of a conversation: arrays a later range of the conversation extends in
// place when it keeps the whole list, past the length any range handed out, so a handler still reading an earlier range sees
// no change.
type sentRange struct {
	store   *history.Store
	settled uint64 // the history lineage of the settled messages (history.View.Settled)
	nSet    int    // how many of the messages were settled
	lineage uint64
	msgs    [][]byte
	idents  []history.MessageIdent
	stored  []bool
}

// rangeContext derives the model messages of conversation conv's committed context cut off at entry tail under head marker
// entry head (0: none), with the transcript store the core uses (history.Store, spec section 2.1) over the records the host
// reads. The store keeps what it loaded, so a later request of the conversation reads only the entries after the ones it
// holds.
func (h *Host) rangeContext(conv, head, tail uint32) (*MessageRange, error) {
	view, err := h.contextView(int64(conv), int64(tail))
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, abi.ErrRecipeRecord
	}
	if view.Tail() != int64(tail) || view.HasHead() != (head != 0) || (head != 0 && view.HeadID() != int64(head)) {
		return nil, abi.ErrRecipeRecord
	}
	n := view.NumMessages()
	settled, nSet := view.Settled()
	s := h.sent[int64(conv)]
	// same is how many leading messages the conversation's last range holds unchanged: the settled ones of the same
	// lineage without a look, then the others by identity (the open part, from the last assistant message on).
	same := 0
	if s != nil && s.store == h.ranges {
		if settled != 0 && s.settled == settled {
			same = min(s.nSet, nSet)
		}
		for same < n && same < len(s.idents) {
			id, stored := view.MessageIdent(same)
			if !stored || !s.stored[same] || id != s.idents[same] {
				break
			}
			same++
		}
	}
	if s == nil || same < len(s.idents) {
		// A list that changes before its end goes to new arrays: a handler may still read the old ones.
		h.rangeLineages++
		next := &sentRange{store: h.ranges, lineage: h.rangeLineages, msgs: make([][]byte, same, n), idents: make([]history.MessageIdent, same, n), stored: make([]bool, same, n)}
		if same > 0 {
			copy(next.msgs, s.msgs[:same])
			copy(next.idents, s.idents[:same])
			copy(next.stored, s.stored[:same])
		}
		s = next
		if h.sent == nil {
			h.sent = map[int64]*sentRange{}
		}
		h.sent[int64(conv)] = s
	}
	s.settled, s.nSet = settled, nSet
	for i := same; i < n; i++ {
		id, stored := view.MessageIdent(i)
		s.msgs = append(s.msgs, view.Message(i))
		s.idents = append(s.idents, id)
		s.stored = append(s.stored, stored)
	}
	return &MessageRange{Conv: int64(conv), Lineage: s.lineage, Msgs: s.msgs[:n:n], Idents: s.idents[:n:n], Stored: s.stored[:n:n]}, nil
}

// answerNeed runs the read a derivation asked for and gives the rows to the store (history.Need).
func (h *Host) answerNeed(need history.Need) error {
	params := need.Params()
	vals := make([]abi.Value, len(params))
	for i, p := range params {
		vals[i] = abi.Int(p)
	}
	rows, _, err := h.opts.DB.All(h.ctx, need.SQL(), vals)
	if err != nil {
		return err
	}
	h.count(func(c *Counters) { c.ContextReads++; c.ContextRows += len(rows) })
	switch need.Kind {
	case history.NeedConversation:
		if len(rows) == 0 {
			return abi.ErrRecipeRecord
		}
		c, err := history.ParseConversation(rows[0][0].Bytes)
		if err != nil {
			return err
		}
		h.ranges.AddConversation(c, false)
	case history.NeedBounds:
		var maxID, markerID, markerHead int64
		var hasMax, hasMarker bool
		if len(rows) != 0 {
			r := rows[0]
			if r[0].Kind != abi.ValueNull {
				hasMax, maxID = true, r[0].Int
			}
			if r[1].Kind != abi.ValueNull {
				hasMarker, markerID, markerHead = true, r[1].Int, r[2].Int
			}
		}
		h.ranges.SupplyBounds(need.Conv, need.Max, hasMax, maxID, hasMarker, markerID, markerHead)
	case history.NeedEntries:
		h.ranges.BeginLoad(need.Conv, need.Min, need.Max)
		for _, r := range rows {
			if err := h.ranges.AddRow(need.Conv, r[0].Int, r[1].Int, r[2].Bytes); err != nil {
				return err
			}
		}
		return h.ranges.EndLoad(need.Conv)
	}
	return nil
}
