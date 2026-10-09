// SPDX-License-Identifier: MIT

package history

import (
	"math"
	"slices"
)

// Policy is the part of the Harness compaction settings that range selection and the thresholds read
// (spec section 8.3 and 8.7).
type Policy struct {
	Enabled          bool
	ReserveTokens    float64
	KeepRecentTokens float64
	BackgroundTokens float64
}

// Threshold says which compaction preparation starts before a request.
type Threshold uint8

// Threshold values.
const (
	// ThresholdNone starts no compaction.
	ThresholdNone Threshold = iota
	// ThresholdBlocking starts a compaction the generation waits for.
	ThresholdBlocking
	// ThresholdBackground starts a conversation-owned compaction the generation does not wait for.
	ThresholdBackground
)

func (v *View) contribRange(i int) (from, to int) { return int(v.d.cOff[i]), int(v.d.cOff[i+1]) }

// toolCallIDs returns the call id spans of the last assistant message among message refs, with the message.
func (v *View) lastAssistantIn(i int) (mref, bool) {
	from, to := v.contribRange(i)
	for k := to - 1; k >= from; k-- {
		if v.st.role(v.d.contrib[k]) == roleAssistant {
			return v.d.contrib[k], true
		}
	}
	return mref{}, false
}

// isCandidate reports whether active entry index can start the kept range: its contribution begins with an
// assistant message, or with a user message that no result of the preceding assistant's calls still follows
// (src/harness/compaction.ts isCandidate, spec section 8.7 rule 1).
func (v *View) isCandidate(index int) bool {
	st := v.st
	from, to := v.contribRange(index)
	if from == to {
		return false
	}
	switch st.role(v.d.contrib[from]) {
	case roleAssistant:
		return true
	case roleUser:
	default:
		return false
	}
	var asst mref
	has := false
	for before := index - 1; before >= 0 && !has; before-- {
		asst, has = v.lastAssistantIn(before)
	}
	if !has {
		return true
	}
	calls := st.callSpans(asst)
	if len(calls) == 0 {
		return true
	}
	n := len(v.d.active)
	for after := index; after < n; after++ {
		f, t := v.contribRange(after)
		for k := f; k < t; k++ {
			m := v.d.contrib[k]
			switch st.role(m) {
			case roleAssistant:
				if after > index || k > f {
					return true
				}
			case roleToolResult:
				id := st.callSpans(m)[0]
				for _, c := range calls {
					if st.sameID(asst, c, m, id) {
						return false
					}
				}
			}
		}
	}
	return true
}

// SelectCut chooses the first entry a summary keeps: the index in the view's active entries, or false when there is
// nothing to compact (src/harness/compaction.ts selectCut; spec section 8.7 "Range selection").
func (v *View) SelectCut(keepRecentTokens float64) (int, bool) {
	n := len(v.d.active)
	start := 0
	if v.d.hasHead {
		start = 1
	}
	var cands []int
	for i := start; i < n; i++ {
		if v.isCandidate(i) {
			cands = append(cands, i)
		}
	}
	kept := 0.0
	cut := -1
	for i := n - 1; i >= start; i-- {
		from, to := v.contribRange(i)
		for k := from; k < to; k++ {
			kept += EstimateMessageTokens(v.st.msgBytes(v.d, v.d.contrib[k]))
		}
		if kept < keepRecentTokens {
			continue
		}
		for _, c := range cands {
			if c >= i {
				cut = c
				break
			}
		}
		if cut < 0 && len(cands) > 0 {
			cut = cands[len(cands)-1]
		}
		break
	}
	if cut < 0 {
		return 0, false
	}
	for i := start; i < cut; i++ {
		if from, to := v.contribRange(i); to > from {
			return cut, true
		}
	}
	return 0, false
}

// SummarizedMessages returns the model messages of the entries before cut, ordered like model context: the head
// marker first, tool results behind their calls, missing results synthesized.
func (v *View) SummarizedMessages(cut int) [][]byte {
	to := int(v.d.cOff[cut])
	var tmp derived
	ordered := v.st.orderToolResults(&tmp, nil, v.d.contrib[:to], synthSettled)
	out := make([][]byte, len(ordered))
	for i, m := range ordered {
		if m.ch == synthSettled {
			out[i] = tmp.synth[m.idx]
		} else {
			out[i] = v.st.msgBytes(v.d, m)
		}
	}
	return out
}

// EstimateContext is the size of a request over the view followed by extra messages (src/harness/compaction.ts
// estimateContext, spec section 8.3): the usage of the newest assistant message appended after the head marker plus
// estimates of the messages after it, or estimates of every message when there is none.
func (v *View) EstimateContext(extra [][]byte) float64 {
	st := v.st
	var measured mref
	has := false
	after := int64(math.MinInt64)
	if v.d.hasHead {
		after = v.d.headID
	}
	for i := len(v.d.active) - 1; i >= 0 && !has; i-- {
		if st.ent(v.d.active[i]).id <= after {
			continue
		}
		from, to := v.contribRange(i)
		for k := to - 1; k >= from; k-- {
			m := v.d.contrib[k]
			if st.role(m) == roleAssistant && CalculateContextTokens(st.msgBytes(v.d, m)) > 0 {
				measured, has = m, true
				break
			}
		}
	}
	first := 0
	tokens := 0.0
	if has {
		tokens = CalculateContextTokens(st.msgBytes(v.d, measured))
		first = 0
		for i, v0 := range slices.Backward(v.d.out) {
			if v0 == measured {
				first = i + 1
				break
			}
		}
	}
	for _, m := range v.d.out[first:] {
		tokens += EstimateMessageTokens(st.msgBytes(v.d, m))
	}
	for _, m := range extra {
		tokens += EstimateMessageTokens(m)
	}
	return tokens
}

// ThresholdCompaction decides which compaction preparation starts before its request (src/harness/generation.ts
// thresholdCompaction, spec section 8.3): blocking above contextWindow - reserveTokens, background above the
// background threshold, and only when range selection finds a cut. planned holds the model messages of the entries
// the preparation is about to append.
func (v *View) ThresholdCompaction(planned [][]byte, contextWindow float64, p Policy) Threshold {
	if !p.Enabled || contextWindow <= 0 {
		return ThresholdNone
	}
	tokens := v.EstimateContext(planned)
	blocking := contextWindow - p.ReserveTokens
	background := blocking - p.BackgroundTokens
	over := ThresholdNone
	switch {
	case tokens > blocking:
		over = ThresholdBlocking
	case p.BackgroundTokens > 0 && tokens > background:
		over = ThresholdBackground
	}
	if over == ThresholdNone {
		return ThresholdNone
	}
	if _, ok := v.SelectCut(p.KeepRecentTokens); !ok {
		return ThresholdNone
	}
	return over
}
