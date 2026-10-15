// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// The context reads of the core (ABI section 4.2). The host stores the entry records, so it derives the context itself; this
// host does it with the core's own derivation (history.Store) over its database.

// contextView derives the context of conversation conv through entry tail with the host's range store.
func (h *Host) contextView(conv, tail int64) (*history.View, error) {
	// A context through the newest entry loads an unbounded chain, which a later commit extends (observeRanges); one
	// through an older entry loads a chain bounded at it, which the next commit discards. A request names the newest
	// entry nearly always, so try the newest first and fall back to the named one for an earlier cutoff (time travel).
	if tail != 0 {
		v, err := h.deriveView(conv, 0)
		if err != nil || (v != nil && v.Tail() == tail) {
			return v, err
		}
	}
	return h.deriveView(conv, tail)
}

func (h *Host) deriveView(conv, tail int64) (*history.View, error) {
	if h.ranges == nil {
		h.ranges = history.NewStore()
		h.ranges.SetTrimAtHead()
	}
	for range 1 << 16 {
		v, need, err := h.ranges.Context(conv, tail)
		if err != nil || need == nil {
			return v, err
		}
		if err := h.answerNeed(*need); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("sqlhost: the context derivation did not converge")
}

// isContextRead reports whether a statement of the core's table is one of the context reads, which this host answers from the
// context it derives instead of running it as SQL.
func isContextRead(text string) bool {
	return text == store.TextContextSummary || text == store.TextContextMessages || text == store.TextContextRelease
}

// answerContext runs one context read (isContextRead) over the host's range store, the one the model requests that name a
// range read through (rangeMessages), so a request and the reads that prepare it share what the store loaded.
func (h *Host) answerContext(text string, params []abi.Value) ([][]abi.Value, error) {
	if text == store.TextContextRelease {
		if h.ranges != nil {
			h.ranges.Release(int64(params[0].Number()))
		}
		delete(h.sent, int64(params[0].Number()))
		return nil, nil
	}
	conv, tail := int64(params[0].Number()), int64(params[1].Number())
	v, err := h.contextView(conv, tail)
	if err != nil {
		return nil, err
	}
	if v.Tail() != tail {
		return nil, fmt.Errorf("the context of conversation %d through entry %d is not in storage", conv, tail)
	}
	if err := sameHead(v, int64(params[2].Number()), int64(params[3].Number())); err != nil {
		return nil, err
	}
	if text == store.TextContextMessages {
		out := []byte{'['}
		for i := 0; i < v.NumMessages(); i++ {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, v.Message(i)...)
		}
		return [][]abi.Value{{abi.Blob(append(out, ']'))}}, nil
	}
	out := append([]byte(`{"tail":`), jv.AppendNumber(nil, float64(tail))...)
	if v.HasHead() {
		out = append(out, `,"head":`...)
		out = jv.AppendNumber(out, float64(v.HeadID()))
		out = append(out, `,"entries":[`...)
		for i := 0; i < v.NumEntries(); i++ {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, '[')
			out = jv.AppendNumber(out, float64(v.EntryID(i)))
			out = append(out, ',')
			out = jv.AppendString(out, v.EntryKindString(i))
			out = append(out, ']')
		}
		out = append(out, ']')
	} else {
		out = append(out, `,"head":null`...)
	}
	out = append(out, `,"system":[`...)
	for i := 0; i < v.NumSystemMessages(); i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, v.SystemMessage(i)...)
	}
	tokens := 0.0
	if params[4].Number() == 1 {
		tokens = v.EstimateContext(nil)
	}
	out = append(out, `],"tokens":`...)
	out = jv.AppendNumber(out, tokens)
	return [][]abi.Value{{abi.Blob(append(out, '}'))}}, nil
}

// ContextView answers a context read the core names instead of building (ABI section 8.1, `{"$context":{conversationId, tail}}`):
// the context of conversation conv through entry tail, derived on the host's goroutine from the range store the core's context
// reads use, rendered as Pi's ContextView.
func (h *Host) ContextView(ctx context.Context, conv, tail int64) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	reply := make(chan result, 1)
	if !h.enqueue(func(dead error) {
		if dead != nil {
			reply <- result{err: dead}
			return
		}
		v, err := h.contextView(conv, tail)
		if err != nil {
			reply <- result{err: err}
			return
		}
		if v.Tail() != tail {
			reply <- result{err: fmt.Errorf("the context of conversation %d through entry %d is not in storage", conv, tail)}
			return
		}
		reply <- result{out: viewJSON(v)}
	}) {
		return nil, h.closedErr()
	}
	select {
	case r := <-reply:
		return r.out, r.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// viewJSON renders a context view as Pi's ContextView.
func viewJSON(v *history.View) []byte {
	out := []byte{'{'}
	if v.HasHead() && v.NumEntries() > 0 {
		out = append(out, `"head":`...)
		out = append(out, v.EntryRecord(0)...)
		out = append(out, ',')
	}
	out = append(out, `"entries":[`...)
	for i := range v.NumEntries() {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, v.EntryRecord(i)...)
	}
	out = append(out, `],"contributions":[`...)
	for i := range v.NumEntries() {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '[')
		for j := range v.NumContributions(i) {
			if j > 0 {
				out = append(out, ',')
			}
			out = append(out, v.Contribution(i, j)...)
		}
		out = append(out, ']')
	}
	out = append(out, `],"messages":[`...)
	for i := range v.NumMessages() {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, v.Message(i)...)
	}
	return append(out, ']', '}')
}

// observeRanges keeps the range store in step with a commit the database applied: a conversation it sees created starts
// complete and an entry extends its chain, so a context read after a commit reads only what the store lacks.
func (h *Host) observeRanges(commit abi.Commit) error {
	if h.ranges == nil {
		return nil
	}
	for _, stmt := range commit.Stmts {
		if int(stmt.SQL) >= len(h.opts.SQL) {
			continue
		}
		switch h.opts.SQL[stmt.SQL] {
		case history.SQLInsertConversation:
			rec, err := history.ParseConversation(stmt.Params[3].Bytes)
			if err != nil {
				return err
			}
			h.ranges.AddConversation(rec, true)
		case history.SQLInsertEntry:
			if err := h.ranges.Append(int64(stmt.Params[1].Number()), int64(stmt.Params[0].Number()), commit.Seq, stmt.Params[4].Bytes); err != nil {
				return err
			}
		}
	}
	return nil
}

// sameHead checks the head marker the core named for a context read (id 0: none) and the marker's head against the context
// this host derived through the same tail. The core captures the bounds from its own state (context.ts captureContextBounds),
// so a mismatch is a core or storage fault, not a context to answer.
func sameHead(v *history.View, id, from int64) error {
	var gotID, gotFrom int64
	if v.HasHead() {
		var marker struct {
			Head int64 `json:"head"`
		}
		if err := json.Unmarshal(v.EntryRecord(0), &marker); err != nil {
			return err
		}
		gotID, gotFrom = v.HeadID(), marker.Head
	}
	if gotID != id || gotFrom != from {
		return fmt.Errorf("the core named head marker %d (head %d) through entry %d; storage has head marker %d (head %d)", id, from, v.Tail(), gotID, gotFrom)
	}
	return nil
}
