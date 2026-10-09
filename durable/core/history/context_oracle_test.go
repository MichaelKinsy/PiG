// SPDX-License-Identifier: MIT

package history

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// formatView renders a view in the oracle's text form (oracle/gen_context.mjs).
func formatView(v *View) string {
	b := []byte(`{"head":`)
	if v.HasHead() {
		b = strconv.AppendInt(b, v.HeadID(), 10)
	} else {
		b = append(b, "null"...)
	}
	b = append(b, `,"entries":[`...)
	for i := 0; i < v.NumEntries(); i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, v.EntryID(i), 10)
	}
	b = append(b, `],"contributions":[`...)
	for i := 0; i < v.NumEntries(); i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '[')
		for j := 0; j < v.NumContributions(i); j++ {
			if j > 0 {
				b = append(b, ',')
			}
			b = append(b, v.Contribution(i, j)...)
		}
		b = append(b, ']')
	}
	b = append(b, `],"messages":[`...)
	for i := 0; i < v.NumMessages(); i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, v.Message(i)...)
	}
	return string(append(b, "]}"...))
}

func parseInt(t testing.TB, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestContextOracle replays the script oracle/gen_context.mjs wrote from pi-durable main's MemoryStorage and
// deriveContext, and requires every derived context to match byte for byte: head, active entries, contributions
// after edits and excluded stop reasons, and the ordered messages with synthesized results (spec section 2.1).
func TestContextOracle(t *testing.T) {
	for file, minExtended := range map[string]int{"context.hex": 100, "real.hex": 1, "real104.hex": 1} {
		t.Run(file, func(t *testing.T) { replayWarm(t, file, minExtended) })
	}
}

func replayWarm(t *testing.T, file string, minExtended int) {
	lines := corpusLines(t, file)
	st := NewStore()
	queries := 0
	var last *View
	for n, f := range lines {
		switch f[0] {
		case "S":
			compareCompaction(t, n, last, f[1], f[2])
		case "C":
			rec, err := ParseConversation(unhex(t, f[1]))
			if err != nil {
				t.Fatal(err)
			}
			st.AddConversation(rec, true)
		case "E":
			conv, seq := parseInt(t, f[1]), parseInt(t, f[2])
			rec := unhex(t, f[3])
			sc, err := scanRecord(rec)
			if err != nil {
				t.Fatalf("line %d: %v", n, err)
			}
			if err := st.Append(conv, sc.id, seq, rec); err != nil {
				t.Fatalf("line %d: %v", n, err)
			}
		case "Q":
			queries++
			conv, at := parseInt(t, f[1]), parseInt(t, f[2])
			v, need, err := st.Context(conv, at)
			last = v
			if need != nil {
				t.Fatalf("line %d: unexpected read %+v", n, *need)
			}
			if f[3][0] == '!' {
				want := string(unhex(t, f[3][1:]))
				if err == nil || err.Error() != want {
					t.Fatalf("line %d: err = %v, want %q", n, err, want)
				}
				continue
			}
			if err != nil {
				t.Fatalf("line %d: %v", n, err)
			}
			got := formatView(v)
			sum := sha256.Sum256([]byte(got))
			if hex.EncodeToString(sum[:]) != f[3] {
				path := t.TempDir() + "/got.json"
				_ = os.WriteFile(path, []byte(got), 0o600)
				t.Fatalf("line %d (conv %d at %d): derived context differs from pi-durable main; got written to %s (regenerate the corpus with DUMP=1 to see the expected text)", n, conv, at, path)
			}
		}
	}
	if queries < 10 {
		t.Fatalf("only %d queries", queries)
	}
	if strings.HasPrefix(file, "real") {
		var heads, edits, forks int
		for _, ch := range st.chainList {
			heads += len(ch.heads)
			edits += len(ch.edits)
		}
		for _, cs := range st.convs {
			if cs.rec.HasParent {
				forks++
			}
		}
		for _, k := range []string{"pi.user", "pi.assistant", "pi.tool-result", "pi.system", "pi.compaction", "pi.reset"} {
			found := false
			for _, known := range st.kinds {
				found = found || string(known) == k
			}
			if !found {
				t.Errorf("the real session wrote no %s entry", k)
			}
		}
		if heads < 3 || edits < 2 || forks < 2 {
			t.Errorf("real session coverage: %d head markers, %d edits, %d forks", heads, edits, forks)
		}
	}
	if st.extended < minExtended {
		t.Fatalf("the replay extended a cached context %d times; the incremental path is not exercised", st.extended)
	}
}

// TestContextOracleCold derives every oracle context from a fresh store that reads only what the Need protocol asks
// for (ADR-0001 D5), and requires the same bytes as the warm replay.
func TestContextOracleCold(t *testing.T) {
	for _, file := range []string{"context.hex", "real.hex", "real104.hex"} {
		t.Run(file, func(t *testing.T) { replayCold(t, file) })
	}
}

func replayCold(t *testing.T, file string) {
	lines := corpusLines(t, file)
	db := newFakeDB()
	queries := 0
	var last *View
	for n, f := range lines {
		switch f[0] {
		case "S":
			compareCompaction(t, n, last, f[1], f[2])
		case "C":
			rec := unhex(t, f[1])
			parsed, err := ParseConversation(rec)
			if err != nil {
				t.Fatal(err)
			}
			db.convs[parsed.ID] = rec
		case "E":
			db.addEntry(t, parseInt(t, f[1]), parseInt(t, f[2]), unhex(t, f[3]))
		case "Q":
			queries++
			conv, at := parseInt(t, f[1]), parseInt(t, f[2])
			st := NewStore()
			v, err := db.context(t, st, conv, at)
			last = v
			if f[3][0] == '!' {
				want := string(unhex(t, f[3][1:]))
				if err == nil || err.Error() != want {
					t.Fatalf("line %d: err = %v, want %q", n, err, want)
				}
				continue
			}
			if err != nil {
				t.Fatalf("line %d: %v", n, err)
			}
			got := formatView(v)
			sum := sha256.Sum256([]byte(got))
			if hex.EncodeToString(sum[:]) != f[3] {
				t.Fatalf("line %d (conv %d at %d): cold derivation differs from pi-durable main:\n%s", n, conv, at, got)
			}
		}
	}
	if queries < 10 {
		t.Fatalf("only %d queries", queries)
	}
}

// compactionText renders the compaction results the oracle's S lines hash (oracle/gen_context.mjs).
func compactionText(t testing.TB, v *View, keeps []float64) string {
	t.Helper()
	b := []byte(`{"cuts":[`)
	cuts := []int{}
	for i, keep := range keeps {
		if i > 0 {
			b = append(b, ',')
		}
		c, ok := v.SelectCut(keep)
		if !ok {
			c = -1
		}
		cuts = append(cuts, c)
		b = strconv.AppendInt(b, int64(c), 10)
	}
	b = append(b, `],"est":[`...)
	for i, extra := range [][][]byte{nil, {[]byte(`{"role":"user","content":"hello world","timestamp":1}`)}} {
		if i > 0 {
			b = append(b, ',')
		}
		e := v.EstimateContext(extra)
		if math.IsNaN(e) {
			b = append(b, "null"...)
		} else {
			b, _ = AppendNumber(b, e)
		}
	}
	var ser, prompt []byte
	for _, c := range cuts {
		if c >= 0 {
			msgs := v.SummarizedMessages(c)
			ser = SerializeConversation(msgs)
			prompt = SummaryPrompt(msgs, []byte("focus \U0001F600 \xED\xA0\x80"), true)
			break
		}
	}
	b = append(b, `],"ser":`...)
	b = appendString(b, ser)
	b = append(b, `,"prompt":`...)
	b = appendString(b, prompt)
	return string(append(b, '}'))
}

func compareCompaction(t testing.TB, line int, v *View, want, keepList string) {
	t.Helper()
	var keeps []float64
	for k := range strings.SplitSeq(keepList, ",") {
		keeps = append(keeps, parseFloatField(t, k))
	}
	got := compactionText(t, v, keeps)
	sum := sha256.Sum256([]byte(got))
	if hex.EncodeToString(sum[:]) != want {
		path := t.TempDir() + "/got.json"
		_ = os.WriteFile(path, []byte(got), 0o600)
		t.Fatalf("line %d: compaction results differ from pi-durable main; got written to %s", line, path)
	}
}
