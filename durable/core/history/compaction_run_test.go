// SPDX-License-Identifier: MIT

package history

import (
	"testing"
)

// TestCompactionRunOfARealSession replays the compaction runs a real pi-durable Harness performed (oracle/
// gen_real.mjs, W lines): from the committed context at the run's tail, the cut, the summarization request the
// summarizer received, and the entry the task placed must all be reproduced byte for byte.
func TestCompactionRunOfARealSession(t *testing.T) {
	st := NewStore()
	runs := 0
	for n, f := range corpusLines(t, "real.hex") {
		switch f[0] {
		case "C":
			rec, err := ParseConversation(unhex(t, f[1]))
			if err != nil {
				t.Fatal(err)
			}
			st.AddConversation(rec, true)
		case "E":
			rec := unhex(t, f[3])
			sc, err := scanRecord(rec)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Append(parseInt(t, f[1]), sc.id, parseInt(t, f[2]), rec); err != nil {
				t.Fatal(err)
			}
		case "W":
			runs++
			conv, tail := parseInt(t, f[1]), parseInt(t, f[2])
			var instr []byte
			hasInstr := f[3] != "-"
			if hasInstr {
				instr = jsonString(t, unhex(t, f[3]))
			}
			captured, entry := unhex(t, f[4]), unhex(t, f[5])
			seq := parseInt(t, f[6])
			ent, err := scanRecord(entry)
			if err != nil || !ent.hasID || ent.flags&flagHead == 0 {
				t.Fatalf("line %d: compaction entry %v %+v", n, err, ent)
			}

			// The select phase sees the committed context at its tail: the entry just before the summary.
			v, need, err := st.Context(conv, tail)
			if need != nil || err != nil {
				t.Fatalf("line %d: %v %v", n, need, err)
			}
			policy := Policy{Enabled: false, ReserveTokens: 1000, KeepRecentTokens: 150}
			plan := PlanSelect(v, &SelectParams{
				ModelPresent: true, ModelKnown: true, Model: []byte(`{"provider":"faux","modelId":"faux-1"}`),
				ThinkingLevel: []byte(`"off"`), StreamOptions: []byte(`{"cacheRetention":"none"}`), ModelMaxTokens: 900, Policy: policy,
			})
			if plan.Outcome != SelectSummarize || plan.FirstKept != ent.head || plan.Tail != tail {
				t.Fatalf("line %d: plan %+v, the run kept %d with tail %d", n, plan, ent.head, tail)
			}
			if plan.Checkpoint.Request.MaxTokens != 800 {
				t.Fatalf("maxTokens %v", plan.Checkpoint.Request.MaxTokens)
			}

			// The summarize phase rebuilds the same context at the pinned tail and cuts at firstKept.
			cut := v.CutOf(plan.FirstKept)
			if cut != plan.Cut {
				t.Fatalf("line %d: cut %d vs %d", n, cut, plan.Cut)
			}
			now := firstTimestamp(t, captured)
			req, err := AppendSummaryContext(nil, v.SummarizedMessagesBefore(cut), instr, hasInstr, now)
			if err != nil {
				t.Fatal(err)
			}
			if string(req) != string(captured) {
				t.Fatalf("line %d: summarization request\n got  %s\n want %s", n, req, captured)
			}

			// The summary the task placed is the wrapped text of its model message.
			summary := summaryOfEntry(t, entry)
			placed := entryTimestamp(t, entry)
			draft, err := AppendSummaryEntryDraft(nil, plan.FirstKept, summary, reasonOf(t, entry), placed)
			if err != nil {
				t.Fatal(err)
			}
			var task int64
			hasTask := ent.hasByTask
			if hasTask {
				task = ent.byTask
			}
			got, err := AppendEntryRecord(nil, draft, ent.id, conv, task, hasTask)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(entry) {
				t.Fatalf("line %d: compaction entry\n got  %s\n want %s", n, got, entry)
			}
			_ = seq
		}
	}
	if runs != 2 {
		t.Fatalf("%d compaction runs in the corpus", runs)
	}
}

func firstTimestamp(t testing.TB, ctx []byte) float64 {
	t.Helper()
	msgs := splitArray(t, fieldOf(t, ctx, "messages"))
	return numberOf(t, fieldOf(t, msgs[0], "timestamp"))
}

func entryTimestamp(t testing.TB, rec []byte) float64 {
	t.Helper()
	msgs := splitArray(t, fieldOf(t, rec, "model"))
	return numberOf(t, fieldOf(t, msgs[0], "timestamp"))
}

func reasonOf(t testing.TB, rec []byte) string {
	t.Helper()
	return string(jsonString(t, fieldOf(t, fieldOf(t, rec, "data"), "reason")))
}

// summaryOfEntry unwraps the summary from a compaction entry's user message.
func summaryOfEntry(t testing.TB, rec []byte) []byte {
	t.Helper()
	msgs := splitArray(t, fieldOf(t, rec, "model"))
	blocks := splitArray(t, fieldOf(t, msgs[0], "content"))
	text := jsonString(t, fieldOf(t, blocks[0], "text"))
	pre, suf := []byte(summaryPrefix), []byte(summarySuffix)
	if len(text) < len(pre)+len(suf) || string(text[:len(pre)]) != string(pre) || string(text[len(text)-len(suf):]) != string(suf) {
		t.Fatalf("summary text %q lacks the wrapper", text)
	}
	return text[len(pre) : len(text)-len(suf)]
}

func fieldOf(t testing.TB, obj []byte, name string) []byte {
	t.Helper()
	s := newScanner(obj)
	it, err := s.object()
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for it.next() {
		if it.keyIs(name) {
			out = it.value()
		}
	}
	if out == nil {
		t.Fatalf("no field %q in %.80s", name, obj)
	}
	return out
}

func numberOf(t testing.TB, b []byte) float64 {
	t.Helper()
	f, err := parseFloat(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
