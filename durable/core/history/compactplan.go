// SPDX-License-Identifier: MIT

package history

// The decisions of the built-in pi.compaction task (src/harness/compaction.ts, spec section 8.7) as pure functions of
// their inputs. The scheduler owns the task record, the commits, the hooks, the sleep and the usage ledger; it
// calls these where the task body decides, and applies what they return.

// SelectOutcome says what the select phase does.
type SelectOutcome uint8

// Outcomes of the select phase.
const (
	// SelectFailNoModel fails the task with reason no_model; Plan.FailText is the message.
	SelectFailNoModel SelectOutcome = iota
	// SelectNothing completes the task with {} because range selection found nothing to compact.
	SelectNothing
	// SelectSummarize moves the task to the summarize phase (or runs the beforeCompact hooks first) with
	// Plan.Checkpoint.
	SelectSummarize
)

// SelectParams are the inputs of the select phase besides the context view.
type SelectParams struct {
	// ModelPresent reports that the resolved agent names a model; ModelKnown that the catalogue has it.
	ModelPresent, ModelKnown bool
	// ModelProvider and ModelID name the model for the failure text when it is unknown.
	ModelProvider, ModelID []byte
	// Model is the agent's ModelRef JSON, ThinkingLevel its thinking level JSON string, StreamOptions the
	// settings' stream options JSON object; they are pinned into the checkpoint as they are.
	Model, ThinkingLevel, StreamOptions []byte
	// ModelMaxTokens is the model's own output limit (0: none).
	ModelMaxTokens float64
	Policy         Policy
}

// SelectPlan is the result of the select phase.
type SelectPlan struct {
	Outcome SelectOutcome
	// FailText is the error message of SelectFailNoModel, a JavaScript string.
	FailText []byte
	// Cut is the index in the view's active entries of the first kept entry; FirstKept its id; Tail the newest id
	// among the active entries and FirstKept.
	Cut       int
	FirstKept int64
	Tail      int64
	// Checkpoint is the summarize checkpoint at attempt 1.
	Checkpoint Checkpoint
}

// PlanSelect runs the select phase over the committed context view (spec section 8.7, "select").
func PlanSelect(v *View, p *SelectParams) SelectPlan {
	if !p.ModelPresent || !p.ModelKnown {
		text := jsText("No model is configured")
		if p.ModelPresent {
			text = jsText("Model ").add(p.ModelProvider).addString("/").add(p.ModelID).addString(" is not available")
		}
		return SelectPlan{Outcome: SelectFailNoModel, FailText: text}
	}
	cut, ok := v.SelectCut(p.Policy.KeepRecentTokens)
	if !ok {
		return SelectPlan{Outcome: SelectNothing}
	}
	firstKept := v.EntryID(cut)
	tail := firstKept
	for i := 0; i < v.NumEntries(); i++ {
		if id := v.EntryID(i); id > tail {
			tail = id
		}
	}
	return SelectPlan{
		Outcome:   SelectSummarize,
		Cut:       cut,
		FirstKept: firstKept,
		Tail:      tail,
		Checkpoint: Checkpoint{Phase: PhaseSummarize, Request: SummaryRequest{
			Attempt:       1,
			Model:         p.Model,
			ThinkingLevel: p.ThinkingLevel,
			StreamOptions: p.StreamOptions,
			MaxTokens:     SummaryMaxTokens(p.Policy.ReserveTokens, p.ModelMaxTokens),
			Tail:          tail,
			FirstKept:     firstKept,
		}},
	}
}

// AppendBeforeCompactPayload appends the payload of the beforeCompact hook (spec section 7.2): the summarized
// entries as stored, their messages, the first kept entry, and the instructions when there are any.
func AppendBeforeCompactPayload(dst []byte, v *View, cut int, reason string, firstKept int64, instructions []byte, hasInstructions bool) []byte {
	dst = append(dst, `{"reason":`...)
	dst = appendString(dst, []byte(reason))
	dst = append(dst, `,"entries":[`...)
	for i := range cut {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, v.EntryRecord(i)...)
	}
	dst = append(dst, `],"messages":[`...)
	for i, m := range v.SummarizedMessages(cut) {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, m...)
	}
	dst = append(dst, `],"firstKept":`...)
	dst = appendInt(dst, firstKept)
	if hasInstructions {
		dst = append(dst, `,"instructions":`...)
		dst = appendString(dst, instructions)
	}
	return append(dst, '}')
}

// CutOf returns the index of entry id among the view's active entries, or -1 (Array.findIndex).
func (v *View) CutOf(id int64) int {
	for i := 0; i < v.NumEntries(); i++ {
		if v.EntryID(i) == id {
			return i
		}
	}
	return -1
}

// SummarizedMessagesBefore is SummarizedMessages for the cut `findIndex` returned: a cut of -1 summarizes every
// entry but the last, as Array.slice(0, -1) does.
func (v *View) SummarizedMessagesBefore(cut int) [][]byte {
	if cut < 0 {
		cut += v.NumEntries()
		if cut < 0 {
			cut = 0
		}
	}
	return v.SummarizedMessages(cut)
}

// AppendSummaryOptions appends the stream options of the summarization request: the pinned stream options without
// `deferred`, then cacheRetention "none", maxTokens, the provider session id and, unless thinking is off, the
// reasoning level (src/harness/compaction.ts summarize). The abort signal is not data. An existing key keeps its
// place and takes the new value.
func AppendSummaryOptions(dst []byte, streamOptions []byte, maxTokens float64, sessionID []byte, thinkingLevel []byte) ([]byte, error) {
	type kv struct{ key, val []byte }
	var fields []kv
	canon, err := Canonical(nil, streamOptions)
	if err != nil {
		return dst, err
	}
	s := newScanner(canon)
	it, err := s.object()
	if err != nil {
		return dst, err
	}
	for it.next() {
		if it.keyIs("deferred") || it.keyIs("signal") {
			continue
		}
		fields = append(fields, kv{it.key, it.value()})
	}
	if it.err != nil {
		return dst, it.err
	}
	set := func(name string, val []byte) {
		for i := range fields {
			if keyIs(fields[i].key, name) {
				fields[i].val = val
				return
			}
		}
		fields = append(fields, kv{[]byte(name), val})
	}
	set("cacheRetention", []byte(`"none"`))
	mt, err := AppendNumber(nil, maxTokens)
	if err != nil {
		return dst, err
	}
	set("maxTokens", mt)
	set("sessionId", appendString(nil, sessionID))
	off := valueKind(thinkingLevel) == '"' && keyIs(thinkingLevel[1:len(thinkingLevel)-1], "off")
	if !off {
		set("reasoning", thinkingLevel)
	}
	dst = append(dst, '{')
	for i, f := range fields {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '"')
		dst = append(dst, f.key...)
		dst = append(dst, '"', ':')
		dst = append(dst, f.val...)
	}
	return append(dst, '}'), nil
}

// SummaryDecision says what a summarization answer does to the task.
type SummaryDecision uint8

// Decisions on a summarization answer.
const (
	// SummaryPlace places Outcome.Summary.
	SummaryPlace SummaryDecision = iota
	// SummaryRetry moves to the retry phase with Outcome.Until.
	SummaryRetry
	// SummaryFail ends the task failed with reason model_error and Outcome.FailText.
	SummaryFail
)

// SummaryOutcome is the decision on a summarization answer.
type SummaryOutcome struct {
	Decision SummaryDecision
	// Summary is the text to place, a JavaScript string.
	Summary []byte
	// Until is the retry's wake time.
	Until float64
	// FailText is the failure message, a JavaScript string.
	FailText []byte
}

// RetryPolicy is the retry setting and the provider-error verdict the summarize phase needs.
type RetryPolicy struct {
	Enabled    bool
	MaxRetries int64
	// Retryable is pi-ai's isRetryableAssistantError(message); DelayMs is retryDelayMs(policy, attempt).
	Retryable bool
	DelayMs   float64
}

// DecideSummary classifies the summarization answer msg of the given attempt (src/harness/compaction.ts summarize):
// a clean stop with text is a summary; a retryable provider error within the retry budget retries after the
// delay; anything else fails. now is the Harness clock when the retry is decided.
func DecideSummary(msg []byte, attempt int64, p RetryPolicy, now float64) SummaryOutcome {
	if text, ok := SummaryText(msg); ok {
		return SummaryOutcome{Decision: SummaryPlace, Summary: text}
	}
	s := newScanner(msg)
	isError := false
	if it, err := s.object(); err == nil {
		for it.next() {
			if it.keyIs("stopReason") {
				isError = false
				if v := it.value(); valueKind(v) == '"' {
					isError = keyIs(v[1:len(v)-1], "error")
				}
			}
		}
	}
	if isError && p.Retryable && p.Enabled && attempt <= p.MaxRetries {
		return SummaryOutcome{Decision: SummaryRetry, Until: now + p.DelayMs}
	}
	return SummaryOutcome{Decision: SummaryFail, FailText: SummaryFailure(msg)}
}

// MessageUsageKey returns `${provider}/${model}` of an assistant message, the key its usage is recorded under.
func MessageUsageKey(msg []byte) []byte {
	var provider, model []byte
	hasP, hasM := false, false
	s := newScanner(msg)
	if it, err := s.object(); err == nil {
		for it.next() {
			switch {
			case it.keyIs("provider"):
				provider, hasP = stringField(it.value())
			case it.keyIs("model"):
				model, hasM = stringField(it.value())
			}
		}
	}
	out := jsText(nil)
	if hasP {
		out = out.add(provider)
	} else {
		out = out.addString("undefined")
	}
	out = out.addString("/")
	if hasM {
		return out.add(model)
	}
	return out.addString("undefined")
}

// MessageUsage returns the usage object of an assistant message as stored.
func MessageUsage(msg []byte) ([]byte, bool) {
	s := newScanner(msg)
	it, err := s.object()
	if err != nil {
		return nil, false
	}
	for it.next() {
		if it.keyIs("usage") {
			return it.value(), valueKind(it.value()) == '{'
		}
	}
	return nil, false
}

// AppendCompactionInput appends the input of a pi.compaction task: {reason, instructions?}.
func AppendCompactionInput(dst []byte, reason string, instructions []byte, hasInstructions bool) []byte {
	dst = append(dst, `{"reason":`...)
	dst = appendString(dst, []byte(reason))
	if hasInstructions {
		dst = append(dst, `,"instructions":`...)
		dst = appendString(dst, instructions)
	}
	return append(dst, '}')
}

// CompactionRequestID is the request id of the write submission a conversation-owned compaction places its summary
// through (`compaction:${taskId}`).
func CompactionRequestID(taskID int64) []byte {
	return appendInt([]byte("compaction:"), taskID)
}
