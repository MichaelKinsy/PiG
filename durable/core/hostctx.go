// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// The context of a model request, derived by the host (ABI section 4.2, context reads). A host that answers the core's reads
// stores the entry records, so it reads the range a context covers, parses it once and derives the active entries and
// messages with pi-durable's range algorithm (src/harness/context.ts). The core names a context by its bounds, the newest
// entry (tail) and the newest head marker at or below it, and asks the host for the little a request preparation reads: the
// system messages, the active entries when a head marker exists, and the estimate of the compaction check. It never reads
// the transcript. The model request then carries the bounds, and the host sends its own message list to the provider.
//
// A core without a reader holds every record and derives contexts itself (ViewContext).

// hostSummary is the host's answer to a context summary read.
type hostSummary struct {
	empty   bool
	hasHead bool
	headID  int64
	entries []turn.PromptEntry
	system  []*jv.Object
	tokens  float64
}

// hostContext is the context of one conversation through one entry, held by the host.
type hostContext struct {
	h                            *handler
	conv, tail, headID, headFrom int64
	summary                      hostSummary
	tokensRead                   bool
}

// hostRead runs one host context read. A failed read makes the handle fatal, like any other failed pig.read.
func (h *handler) hostRead(sql uint16, params ...abi.Value) ([][]abi.Value, bool) {
	return h.mir.lazyRead(sql, params...)
}

// hostContextOf asks the host for the summary of the context through tail, with the marker the core found for it.
// wantTokens asks for the estimate of the compaction check.
func (h *handler) hostContextOf(conv, tail, headID, headFrom int64, wantTokens bool) (*hostContext, bool) {
	c := &hostContext{h: h, conv: conv, tail: tail, headID: headID, headFrom: headFrom}
	if tail == 0 {
		c.summary.empty = true
		return c, true
	}
	want := int64(0)
	if wantTokens {
		want = 1
	}
	rows, ok := h.hostRead(store.SQLContextSummary, abi.Int(conv), abi.Int(tail), abi.Int(headID), abi.Int(headFrom), abi.Int(want))
	if !ok {
		return nil, false
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		h.mir.lazy.err = errors.New("core: the host's context summary is not one row of one column")
		return nil, false
	}
	v, err := jv.Parse(rows[0][0].Bytes)
	o, _ := v.(*jv.Object)
	if err != nil || o == nil {
		h.mir.lazy.err = errors.New("core: the host's context summary is not a JSON object")
		return nil, false
	}
	if int64(numOf(o, "tail")) != tail {
		h.mir.lazy.err = errors.New("core: the host's context summary is for another entry than the one asked")
		return nil, false
	}
	if head, ok := o.Get("head"); ok && head != nil {
		c.summary.hasHead = true
		c.summary.headID = int64(numOf(o, "head"))
	}
	if list, ok := o.Get("entries"); ok {
		items, _ := list.([]any)
		c.summary.entries = make([]turn.PromptEntry, 0, len(items))
		for _, item := range items {
			pair, _ := item.([]any)
			if len(pair) != 2 {
				continue
			}
			id, _ := pair[0].(float64)
			kind, _ := pair[1].(string)
			c.summary.entries = append(c.summary.entries, turn.PromptEntry{ID: session.ID(id), Kind: kind})
		}
	}
	if list, ok := o.Get("system"); ok {
		items, _ := list.([]any)
		for _, item := range items {
			if m, ok := item.(*jv.Object); ok {
				c.summary.system = append(c.summary.system, m)
			}
		}
	}
	c.summary.tokens = numOf(o, "tokens")
	c.tokensRead = wantTokens
	return c, true
}

// PromptView implements turn.RequestContext.
func (c *hostContext) PromptView() turn.PromptView {
	pv := turn.PromptView{HasHead: c.summary.hasHead, HeadID: session.ID(c.summary.headID), Messages: c.summary.system}
	if pv.HasHead {
		pv.Entries = c.summary.entries
	}
	return pv
}

// Bounds implements turn.RequestContext.
func (c *hostContext) Bounds() (head, tail int64) { return c.headID, c.tail }

// SystemMessages implements turn.RequestContext.
func (c *hostContext) SystemMessages() []*jv.Object { return c.summary.system }

// Messages implements turn.RequestContext: the messages are the host's, so a hook that wants them asks for them.
func (c *hostContext) Messages() []*jv.Object {
	if c.summary.empty {
		return nil
	}
	rows, ok := c.h.hostRead(store.SQLContextMessages, abi.Int(c.conv), abi.Int(c.tail), abi.Int(c.headID), abi.Int(c.headFrom))
	if !ok {
		return nil
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		c.h.mir.lazy.err = errors.New("core: the host's context messages are not one row of one column")
		return nil
	}
	v, err := jv.Parse(rows[0][0].Bytes)
	list, _ := v.([]any)
	if err != nil {
		c.h.mir.lazy.err = errors.New("core: the host's context messages are not JSON")
		return nil
	}
	out := make([]*jv.Object, 0, len(list))
	for _, item := range list {
		if o, ok := item.(*jv.Object); ok {
			out = append(out, o)
		}
	}
	return out
}

// Threshold implements turn.RequestContext: generation.ts thresholdCompaction over the host's estimate of the context and the
// estimates of the planned messages. Range selection needs the whole view, so the core derives it, but only when the
// estimate is over a threshold.
func (c *hostContext) Threshold(planned [][]byte, window float64, p history.Policy) history.Threshold {
	if !p.Enabled || window <= 0 {
		return history.ThresholdNone
	}
	if !c.tokensRead && !c.summary.empty {
		// The summary was read while compaction was disabled and a settings change enabled it since: ask for the estimate.
		fresh, ok := c.h.hostContextOf(c.conv, c.tail, c.headID, c.headFrom, true)
		if !ok {
			return history.ThresholdNone
		}
		c.summary.tokens, c.tokensRead = fresh.summary.tokens, true
	}
	tokens := c.summary.tokens
	for _, m := range planned {
		tokens += history.EstimateMessageTokens(m)
	}
	return history.ThresholdOf(tokens, window, p, func() bool {
		view, err := c.h.contextView(c.conv, c.tail)
		if err != nil {
			c.h.mir.lazy.err = err
			return false
		}
		_, ok := view.SelectCut(p.KeepRecentTokens)
		return ok
	})
}

// releaseHostContext tells the host the conversation's kept context is dropped.
func (h *handler) releaseHostContext(conv session.ID) {
	if h.reader != nil {
		h.hostRead(store.SQLContextRelease, abi.Int(int64(conv)))
	}
}
