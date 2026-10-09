// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

// FuzzCanonical checks that Canonical never panics, accepts exactly what the scanner validates, and is idempotent:
// its output is already canonical (CONTRACT section 2.3 / 5.6, encoder).
func FuzzCanonical(f *testing.F) {
	for _, c := range corpusLines(f, "canon.hex")[:300] {
		f.Add(unhex(f, c[0]))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		if !utf8.Valid(in) {
			return // JSON text from JSON.stringify or SQLite TEXT is valid UTF-8
		}
		out, err := Canonical(nil, in)
		if err != nil {
			return
		}
		again, err := Canonical(nil, out)
		if err != nil {
			t.Fatalf("canonical output %q is rejected: %v", out, err)
		}
		if !bytes.Equal(out, again) {
			t.Fatalf("not idempotent: %q then %q", out, again)
		}
	})
}

// FuzzScanRecord checks that indexing an arbitrary record never panics, and that a record that indexes also derives:
// the context of a conversation holding it never panics either.
func FuzzScanRecord(f *testing.F) {
	for _, c := range corpusLines(f, "context.hex") {
		if c[0] == "E" {
			f.Add(unhex(f, c[3]))
			if f.Name() == "" {
				break
			}
		}
	}
	f.Add([]byte(`{"id":3,"conversationId":2,"model":[{"role":"assistant","content":[{"type":"toolCall","id":"a"}],"stopReason":"stop"},{"role":"toolResult","toolCallId":"a"}],"edits":[{"target":3,"action":"replace","messages":[{"role":"user"}]}],"head":3}`))
	f.Fuzz(func(t *testing.T, rec []byte) {
		st := NewStore()
		st.AddConversation(ConvRecord{ID: 2}, true)
		if err := st.Append(2, 3, 1, rec); err != nil {
			return
		}
		sc, _ := scanRecord(rec)
		if sc.id != 3 {
			return
		}
		v, need, err := st.Context(2, 0)
		if need != nil || err != nil {
			return
		}
		for i := 0; i < v.NumMessages(); i++ {
			_ = v.Message(i)
			EstimateMessageTokens(v.Message(i))
		}
		v.SelectCut(1)
		v.EstimateContext(nil)
		v.SummarizedMessagesBefore(v.NumEntries())
	})
}
