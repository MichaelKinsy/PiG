// SPDX-License-Identifier: MIT

package history

// The pi.compaction task checkpoint (src/harness/compaction.ts CompactionCheckpoint, spec section 8.7):
//
//	{"phase":"select"}
//	{"phase":"summarize","attempt","model","thinkingLevel","streamOptions","maxTokens","tail","firstKept"}
//	{"phase":"retry","attempt",...,"firstKept","until"}
//
// The pinned model, thinking level and stream options are carried as the JSON they were stored as.

// CompactionPhase names a phase of the pi.compaction task.
type CompactionPhase uint8

// Phases of the compaction task.
const (
	PhaseSelect CompactionPhase = iota
	PhaseSummarize
	PhaseRetry
)

// SummaryRequest is the pinned summarization request of the summarize and retry phases.
type SummaryRequest struct {
	Attempt int64
	// Model, ThinkingLevel and StreamOptions are JSON values.
	Model         []byte
	ThinkingLevel []byte
	StreamOptions []byte
	MaxTokens     float64
	// Tail is the newest entry of the context the range was selected from; FirstKept is the summary's head.
	Tail      int64
	FirstKept int64
}

// Checkpoint is a decoded compaction checkpoint.
type Checkpoint struct {
	Phase   CompactionPhase
	Request SummaryRequest
	// Until is the retry phase's wake time.
	Until float64
}

// AppendCheckpoint appends the stored form of a compaction checkpoint.
func AppendCheckpoint(dst []byte, c *Checkpoint) ([]byte, error) {
	if c.Phase == PhaseSelect {
		return append(dst, `{"phase":"select"}`...), nil
	}
	if c.Phase == PhaseSummarize {
		dst = append(dst, `{"phase":"summarize"`...)
	} else {
		dst = append(dst, `{"phase":"retry"`...)
	}
	r := &c.Request
	dst = append(dst, `,"attempt":`...)
	dst = appendInt(dst, r.Attempt)
	dst = append(dst, `,"model":`...)
	dst = append(dst, r.Model...)
	dst = append(dst, `,"thinkingLevel":`...)
	dst = append(dst, r.ThinkingLevel...)
	dst = append(dst, `,"streamOptions":`...)
	dst = append(dst, r.StreamOptions...)
	dst = append(dst, `,"maxTokens":`...)
	var err error
	if dst, err = AppendNumber(dst, r.MaxTokens); err != nil {
		return dst, err
	}
	dst = append(dst, `,"tail":`...)
	dst = appendInt(dst, r.Tail)
	dst = append(dst, `,"firstKept":`...)
	dst = appendInt(dst, r.FirstKept)
	if c.Phase == PhaseRetry {
		dst = append(dst, `,"until":`...)
		if dst, err = AppendNumber(dst, c.Until); err != nil {
			return dst, err
		}
	}
	return append(dst, '}'), nil
}

// ParseCheckpoint decodes a stored compaction checkpoint.
func ParseCheckpoint(b []byte) (Checkpoint, error) {
	var c Checkpoint
	s := newScanner(b)
	it, err := s.object()
	if err != nil {
		return c, err
	}
	phase := ""
	for it.next() {
		v := it.value()
		switch {
		case it.keyIs("phase"):
			if p, ok := stringField(v); ok {
				phase = string(p)
			}
		case it.keyIs("attempt"):
			c.Request.Attempt, _ = intValue(v)
		case it.keyIs("model"):
			c.Request.Model = append([]byte(nil), v...)
		case it.keyIs("thinkingLevel"):
			c.Request.ThinkingLevel = append([]byte(nil), v...)
		case it.keyIs("streamOptions"):
			c.Request.StreamOptions = append([]byte(nil), v...)
		case it.keyIs("maxTokens"):
			c.Request.MaxTokens = numberField(v)
		case it.keyIs("tail"):
			c.Request.Tail, _ = intValue(v)
		case it.keyIs("firstKept"):
			c.Request.FirstKept, _ = intValue(v)
		case it.keyIs("until"):
			c.Until = numberField(v)
		}
	}
	if it.err != nil {
		return c, it.err
	}
	switch phase {
	case "select":
		c.Phase = PhaseSelect
	case "summarize":
		c.Phase = PhaseSummarize
	case "retry":
		c.Phase = PhaseRetry
	default:
		return c, ErrShape
	}
	return c, nil
}

// NextAttempt is the retry phase's transition back to summarize (src/harness/compaction.ts retry phase): the same
// pinned request with the attempt count raised by one, and no wake time.
func (c *Checkpoint) NextAttempt() Checkpoint {
	n := *c
	n.Phase = PhaseSummarize
	n.Request.Attempt++
	n.Until = 0
	return n
}
