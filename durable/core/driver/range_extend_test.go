// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// TestRangeMessagesEqualAFullDecodeAndLeaveEarlierListsIntact gives the executor ranges as a host does: a range of the
// previous one's lineage extends its arrays in place, and a range that changes a message before the end starts a new
// lineage over new arrays. Each list must be the decoding of every message of its range, and a list handed out earlier must
// not change while later requests extend or replace it (a stream may still read it).
func TestRangeMessagesEqualAFullDecodeAndLeaveEarlierListsIntact(t *testing.T) {
	text := func(m ai.Message) string { return string(m.(ai.UserMessage).Content.(ai.UserText)) }
	for seed := range uint64(16) {
		rnd := rand.New(rand.NewPCG(seed, 3))
		m := NewModels(nil, func() float64 { return 0 }, nil)
		var texts []string
		var msgs [][]byte
		var idents []history.MessageIdent
		var stored []bool
		lineage := uint64(1)
		type held struct {
			list  []ai.Message
			texts []string
		}
		var lists []held
		for call := range 60 {
			// Identities are all equal here (history.MessageIdent has no exported constructor), so a message that replaces
			// another is given no identity, as a synthesized one, which makes the comparison stop at it.
			replaced := false
			switch op := rnd.IntN(6); {
			case op == 0 && len(texts) > 0:
				// A message before the end changes: the host copies the unchanged prefix to new arrays.
				keep := rnd.IntN(len(texts))
				texts = append([]string(nil), texts[:keep]...)
				msgs = append([][]byte(nil), msgs[:keep]...)
				idents = append([]history.MessageIdent(nil), idents[:keep]...)
				stored = append([]bool(nil), stored[:keep]...)
				lineage++
				replaced = true
			case op == 1:
				// Another lineage with the same messages (a store that derived the context again).
				lineage++
			}
			for range rnd.IntN(4) {
				s := "m" + strconv.Itoa(call) + "_" + strconv.Itoa(len(texts))
				texts = append(texts, s)
				msgs = append(msgs, []byte(`{"role":"user","content":"`+s+`","timestamp":1}`))
				idents = append(idents, history.MessageIdent{})
				stored = append(stored, !replaced && rnd.IntN(5) != 0)
				replaced = false
			}
			n := len(texts)
			rng := &sqlhost.MessageRange{Conv: 1, Lineage: lineage, Msgs: msgs[:n:n], Idents: idents[:n:n], Stored: stored[:n:n]}
			list, err := m.rangeMessages(uint32(call+1), rng)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != n || cap(list) != n {
				t.Fatalf("seed %d call %d: list of len %d cap %d for %d messages", seed, call, len(list), cap(list), n)
			}
			for i := range n {
				if text(list[i]) != texts[i] {
					t.Fatalf("seed %d call %d message %d: %q, want %q", seed, call, i, text(list[i]), texts[i])
				}
			}
			lists = append(lists, held{list: list, texts: append([]string(nil), texts...)})
			for j, h := range lists {
				for i, want := range h.texts {
					if text(h.list[i]) != want {
						t.Fatalf("seed %d call %d: list %d changed at message %d: %q, want %q", seed, call, j, i, text(h.list[i]), want)
					}
				}
			}
		}
	}
}

// TestRangeMessagesTakeASameLineagePrefixWithoutAComparison checks that a range of the previous one's lineage extends the
// kept list without comparing the identities of the messages both have: the host guarantees they agree, and comparing them
// made every request of a turn cost the history. The test spoils the kept stored flags and marks the kept first message: an
// executor that compared the flags would decode the whole range again and lose the mark.
func TestRangeMessagesTakeASameLineagePrefixWithoutAComparison(t *testing.T) {
	text := func(m ai.Message) string { return string(m.(ai.UserMessage).Content.(ai.UserText)) }
	m := NewModels(nil, func() float64 { return 0 }, nil)
	var msgs [][]byte
	var idents []history.MessageIdent
	var stored []bool
	grow := func(add int) *sqlhost.MessageRange {
		for range add {
			msgs = append(msgs, []byte(`{"role":"user","content":"message `+strconv.Itoa(len(msgs))+`","timestamp":1}`))
			idents = append(idents, history.MessageIdent{})
			stored = append(stored, true)
		}
		n := len(msgs)
		return &sqlhost.MessageRange{Conv: 1, Lineage: 1, Msgs: msgs[:n:n], Idents: idents[:n:n], Stored: stored[:n:n]}
	}
	if _, err := m.rangeMessages(1, grow(50)); err != nil {
		t.Fatal(err)
	}
	kept := m.ranges[1]
	kept.stored = make([]bool, len(kept.stored))
	kept.list[0] = ai.UserMessage{Content: ai.UserText("kept")}
	next, err := m.rangeMessages(2, grow(4))
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 54 || text(next[0]) != "kept" || text(next[53]) != "message 53" {
		t.Fatalf("a range of the same lineage gave %d messages, the first %q: the kept prefix was compared and decoded again", len(next), text(next[0]))
	}
}
