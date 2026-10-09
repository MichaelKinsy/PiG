// SPDX-License-Identifier: MIT

package history

import (
	"strconv"
	"testing"
)

// jsonString decodes a JSON string text to a JavaScript string (WTF-8).
func jsonString(t testing.TB, b []byte) []byte {
	t.Helper()
	s, ok := stringField(b)
	if !ok {
		t.Fatalf("not a JSON string: %q", b)
	}
	return s
}

func splitArray(t testing.TB, b []byte) [][]byte {
	t.Helper()
	s := newScanner(b)
	it, err := s.array()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for it.next() {
		out = append(out, b[it.vs:it.ve])
	}
	if it.err != nil {
		t.Fatal(it.err)
	}
	return out
}

func parseFloatField(t testing.TB, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func expectText(t testing.TB, what string, n int, got []byte, wantHex string) {
	t.Helper()
	if want := unhex(t, wantHex); string(got) != string(want) {
		t.Fatalf("%s, case %d:\n got  %s\n want %s", what, n, got, want)
	}
}

// TestSummaryOracle checks the compaction text helpers, entry record encoding and the summary and reset drafts
// against pi-durable main (oracle/gen_summary.mjs).
func TestSummaryOracle(t *testing.T) {
	counts := map[string]int{}
	for n, f := range corpusLines(t, "summary.hex") {
		counts[f[0]]++
		switch f[0] {
		case "T":
			msg := unhex(t, f[1])
			text, ok := SummaryText(msg)
			var got []byte
			if ok {
				got = appendString(nil, text)
			} else {
				got = []byte("null")
			}
			expectText(t, "summaryText", n, got, f[2])
			expectText(t, "summaryFailure", n, appendString(nil, SummaryFailure(msg)), f[3])
		case "D":
			draft := unhex(t, f[1])
			id, conv := parseInt(t, f[2]), parseInt(t, f[3])
			var task int64
			hasTask := f[4] != "-"
			if hasTask {
				task = parseInt(t, f[4])
			}
			got, err := AppendEntryRecord(nil, draft, id, conv, task, hasTask)
			if err != nil {
				t.Fatalf("case %d: %v", n, err)
			}
			expectText(t, "entry record of "+string(draft), n, got, f[5])
			// A stored record must index back to the same id and head.
			sc, err := scanRecord(got)
			if err != nil || sc.id != id || sc.conv != conv {
				t.Fatalf("case %d: scan %v %+v", n, err, sc)
			}
		case "P":
			msgs := splitArray(t, unhex(t, f[1]))
			var instr []byte
			hasInstr := f[2] != "-"
			if hasInstr {
				instr = jsonString(t, unhex(t, f[2]))
			}
			now, err := strconv.ParseFloat(f[3], 64)
			if err != nil {
				t.Fatal(err)
			}
			summary := jsonString(t, unhex(t, f[4]))
			ctx, err := AppendSummaryContext(nil, msgs, instr, hasInstr, now)
			if err != nil {
				t.Fatal(err)
			}
			expectText(t, "summary context", n, ctx, f[7])
			draft, err := AppendSummaryEntryDraft(nil, parseInt(t, f[6]), summary, f[5], now)
			if err != nil {
				t.Fatal(err)
			}
			expectText(t, "summary entry draft", n, draft, f[8])
		case "K":
			sum, retry := unhex(t, f[1]), unhex(t, f[2])
			c, err := ParseCheckpoint(sum)
			if err != nil || c.Phase != PhaseSummarize {
				t.Fatalf("case %d: %v %+v", n, err, c)
			}
			got, err := AppendCheckpoint(nil, &c)
			if err != nil {
				t.Fatal(err)
			}
			expectText(t, "summarize checkpoint", n, got, f[1])
			r, err := ParseCheckpoint(retry)
			if err != nil || r.Phase != PhaseRetry {
				t.Fatalf("case %d: %v %+v", n, err, r)
			}
			got, _ = AppendCheckpoint(nil, &r)
			expectText(t, "retry checkpoint", n, got, f[2])
			nx := r.NextAttempt()
			got, _ = AppendCheckpoint(nil, &nx)
			expectText(t, "retry to summarize checkpoint", n, got, f[3])
		case "O":
			dst, err := AppendSummaryOptions(nil, unhex(t, f[1]), parseFloatField(t, f[2]), jsonString(t, unhex(t, f[3])), unhex(t, f[4]))
			if err != nil {
				t.Fatal(err)
			}
			expectText(t, "summary options", n, dst, f[5])
		case "X":
			msg := unhex(t, f[1])
			out := DecideSummary(msg, parseInt(t, f[2]), RetryPolicy{
				Retryable: f[3] == "1", Enabled: f[4] == "1", MaxRetries: parseInt(t, f[5]), DelayMs: parseFloatField(t, f[6]),
			}, parseFloatField(t, f[7]))
			var got []byte
			switch out.Decision {
			case SummaryPlace:
				got = append(append(got, `{"d":"place","s":`...), appendString(nil, out.Summary)...)
			case SummaryRetry:
				got = append(got, `{"d":"retry","until":`...)
				got, _ = AppendNumber(got, out.Until)
			case SummaryFail:
				got = append(append(got, `{"d":"fail","t":`...), appendString(nil, out.FailText)...)
			}
			expectText(t, "summary decision", n, append(got, '}'), f[8])
		case "R":
			var handoff []byte
			has := f[1] != "-"
			if has {
				handoff = jsonString(t, unhex(t, f[1]))
			}
			now, _ := strconv.ParseFloat(f[2], 64)
			got, err := AppendResetDraft(nil, handoff, has, now)
			if err != nil {
				t.Fatal(err)
			}
			expectText(t, "reset draft", n, got, f[3])
			if !DraftIsReset(got) {
				t.Fatal("reset draft is not a reset")
			}
		}
	}
	if counts["T"] < 500 || counts["D"] < 500 || counts["P"] < 50 || counts["R"] != 4 || counts["K"] != 30 {
		t.Fatalf("corpus too small: %v", counts)
	}
}

// TestEntryRecordsOfARealSession rebuilds each entry row a real pi-durable Session wrote from its draft (the
// record without id, conversationId, head and byTaskId) and requires the same stored bytes.
func TestEntryRecordsOfARealSession(t *testing.T) {
	n := 0
	for _, f := range corpusLines(t, "real.hex") {
		if f[0] != "E" {
			continue
		}
		rec := unhex(t, f[3])
		s := newScanner(rec)
		it, err := s.object()
		if err != nil {
			t.Fatal(err)
		}
		var draft []byte
		draft = append(draft, '{')
		var id, conv, task int64
		var hasTask bool
		headSelf, hasHead := false, false
		var head []byte
		for it.next() {
			switch {
			case it.keyIs("id"):
				id, _ = intValue(it.value())
			case it.keyIs("conversationId"):
				conv, _ = intValue(it.value())
			case it.keyIs("byTaskId"):
				task, hasTask = intValue(it.value())
			case it.keyIs("head"):
				hasHead, head = true, it.value()
			default:
				if len(draft) > 1 {
					draft = append(draft, ',')
				}
				draft = append(draft, '"')
				draft = append(draft, it.key...)
				draft = append(draft, '"', ':')
				draft = append(draft, it.value()...)
			}
		}
		if hasHead {
			if h, _ := intValue(head); h == id {
				headSelf = true
			}
			if len(draft) > 1 {
				draft = append(draft, ',')
			}
			if headSelf {
				draft = append(draft, `"head":"self"`...)
			} else {
				draft = append(draft, `"head":`...)
				draft = append(draft, head...)
			}
		}
		draft = append(draft, '}')
		got, err := AppendEntryRecord(nil, draft, id, conv, task, hasTask)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(rec) {
			t.Fatalf("entry %d:\n got  %s\n want %s", id, got, rec)
		}
		n++
	}
	if n < 20 {
		t.Fatalf("only %d entries", n)
	}
}
