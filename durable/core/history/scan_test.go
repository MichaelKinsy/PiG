// SPDX-License-Identifier: MIT

package history

import (
	"bytes"
	"testing"
)

// pretty inserts whitespace between the tokens of a JSON text, outside strings.
func pretty(b []byte) []byte {
	var out []byte
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		out = append(out, c)
		if inStr {
			switch c {
			case '\\':
				i++
				out = append(out, b[i])
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[', ',':
			out = append(out, "\n\t "...)
		case ':':
			out = append(out, " \r\n"...)
		}
	}
	return out
}

// TestScannerToleratesWhitespaceAndKeyOrder indexes every record of the oracle corpus twice, as stored and
// pretty-printed, and requires the same index: roles, stop reasons, call ids, heads, edits (CONTRACT section 2.3,
// "Reading never fails on ... a different key order").
func TestScannerToleratesWhitespaceAndKeyOrder(t *testing.T) {
	a, b := NewStore(), NewStore()
	n := 0
	for _, f := range corpusLines(t, "context.hex") {
		switch f[0] {
		case "C":
			rec, _ := ParseConversation(unhex(t, f[1]))
			a.AddConversation(rec, true)
			b.AddConversation(rec, true)
		case "E":
			conv, seq := parseInt(t, f[1]), parseInt(t, f[2])
			rec := unhex(t, f[3])
			sc, err := scanRecord(rec)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Append(conv, sc.id, seq, rec); err != nil {
				t.Fatal(err)
			}
			p := pretty(rec)
			if err := b.Append(conv, sc.id, seq, p); err != nil {
				t.Fatalf("pretty record: %v\n%s", err, p)
			}
			n++
		}
	}
	for i := range a.chainList {
		ca, cb := a.chainList[i], b.chainList[i]
		if len(ca.ents) != len(cb.ents) || len(ca.msgs) != len(cb.msgs) || len(ca.spans) != len(cb.spans) ||
			len(ca.heads) != len(cb.heads) || len(ca.edits) != len(cb.edits) {
			t.Fatalf("chain %d: index sizes differ", i)
		}
		for k := range ca.ents {
			x, y := ca.ents[k], cb.ents[k]
			xf, xt := ca.modelMessages(k)
			yf, yt := cb.modelMessages(k)
			if x.id != y.id || x.seq != y.seq || xt-xf != yt-yf || x.kind != y.kind || x.flags != y.flags {
				t.Fatalf("chain %d entry %d: %+v vs %+v", i, k, x, y)
			}
		}
		for k := range ca.msgs {
			x, y := ca.msgs[k], cb.msgs[k]
			if x.role != y.role || x.stop != y.stop || x.nCalls != y.nCalls {
				t.Fatalf("chain %d message %d: %+v vs %+v", i, k, x, y)
			}
			cx, _ := Canonical(nil, ca.msgBytes(&ca.msgs[k]))
			cy, _ := Canonical(nil, cb.msgBytes(&cb.msgs[k]))
			if !bytes.Equal(cx, cy) {
				t.Fatalf("chain %d message %d bytes differ", i, k)
			}
		}
		if len(ca.emsgs) != len(cb.emsgs) {
			t.Fatalf("chain %d: %d vs %d replacement messages", i, len(ca.emsgs), len(cb.emsgs))
		}
		for k := range ca.spans {
			sx, sy := ca.spans[k], cb.spans[k]
			if (sx.off == missingSpan) != (sy.off == missingSpan) ||
				(sx.off != missingSpan && string(ca.spanBytes(sx)) != string(cb.spanBytes(sy))) {
				t.Fatalf("chain %d span %d differs", i, k)
			}
		}
		for k := range ca.heads {
			if ca.heads[k].head != cb.heads[k].head || ca.heads[k].ent != cb.heads[k].ent {
				t.Fatalf("chain %d head %d", i, k)
			}
		}
		for k := range ca.edits {
			x, y := ca.edits[k], cb.edits[k]
			if x.target != y.target || x.omit != y.omit || x.replace != y.replace || x.nMsg != y.nMsg || x.ent != y.ent {
				t.Fatalf("chain %d edit %d", i, k)
			}
		}
	}
	if n < 1000 {
		t.Fatalf("only %d records", n)
	}
}

// TestScannerRejectsMalformedRecords checks that an invalid record fails to index and leaves the chain unchanged.
func TestScannerRejectsMalformedRecords(t *testing.T) {
	st := NewStore()
	st.AddConversation(ConvRecord{ID: 2}, true)
	good := []byte(`{"kind":"pi.user","model":[{"role":"user","content":"x","timestamp":1}],"id":3,"conversationId":2}`)
	if err := st.Append(2, 3, 1, good); err != nil {
		t.Fatal(err)
	}
	ch := st.chainList[0]
	arena, msgs, spans := len(ch.arena), len(ch.msgs), len(ch.spans)
	for _, bad := range []string{
		``, `{`, `[]`, `{"id":4,"model":[{"role":"user"}`, `{"id":4,"model":[1,]}`, `{"id":4}x`,
		`{"id":5,"conversationId":2}`, // the record's id differs from its row's
		`{"id":4,"edits":[{"target":1,"action":"replace","messages":[{"role":"user"]}]}`,
		`{"id":4,"model":[{"role":"assistant","content":[{"type":"toolCall","id":"a"},{"type":"toolCall","id":"b",}]}]}`,
	} {
		if err := st.Append(2, 4, 2, []byte(bad)); err == nil {
			t.Errorf("%q indexed", bad)
		}
		if ch.Len() != 1 || len(ch.arena) != arena || len(ch.msgs) != msgs || len(ch.spans) != spans || len(ch.edits) != 0 || len(ch.emsgs) != 0 {
			t.Fatalf("%q left residue: %d entries, %d arena, %d msgs, %d spans, %d edits", bad, ch.Len(), len(ch.arena), len(ch.msgs), len(ch.spans), len(ch.edits))
		}
	}
	// A row id that does not ascend within its conversation is rejected too.
	if err := st.Append(2, 3, 2, good); err == nil {
		t.Error("a repeated id indexed")
	}
}
