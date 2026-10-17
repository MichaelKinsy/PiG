// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"bytes"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// heldRange is a copy of a range as a handler received it.
type heldRange struct {
	rng    *MessageRange
	msgs   []string
	idents []history.MessageIdent
	stored []bool
}

func holdRange(rng *MessageRange) heldRange {
	h := heldRange{rng: rng, idents: slices.Clone(rng.Idents), stored: slices.Clone(rng.Stored)}
	for _, m := range rng.Msgs {
		h.msgs = append(h.msgs, string(m))
	}
	return h
}

// TestRangesThatExtendTheLastOneMatchAFreshDerivation drives a conversation through random growth: whole turns, a user
// message alone, a tool call whose result comes later (a synthesized result meanwhile), a system message, a compaction
// marker and a release of the store's context. Each range a request takes must be what a host that holds nothing derives;
// a range the host handed out must not change while the host goes on (a handler may still read it); and two ranges with
// the same lineage must agree on every message both have, which is what lets a handler skip comparing that prefix.
func TestRangesThatExtendTheLastOneMatchAFreshDerivation(t *testing.T) {
	for seed := range uint64(24) {
		t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
			rnd := rand.New(rand.NewPCG(seed, 7))
			r := newRangeDB(t)
			r.conversation(2, "")
			var held []heldRange
			var marker int64
			var users []int64
			pending := ""
			for step := range 80 {
				s := strconv.Itoa(step)
				switch op := rnd.IntN(10); {
				case pending != "":
					r.entry(2, "pi.tool-result", `{"role":"toolResult","toolCallId":"`+pending+`","toolName":"read","content":[{"type":"text","text":"late `+s+`"}],"isError":false,"timestamp":`+s+`}`, "", 0)
					pending = ""
				case op < 4:
					users = append(users, r.turn(2, step))
				case op == 4:
					users = append(users, r.entry(2, "pi.user", `{"role":"user","content":"alone `+s+`","timestamp":`+s+`}`, "", 0))
				case op == 5:
					pending = "pending_" + s
					r.entry(2, "pi.assistant", `{"role":"assistant","content":[{"type":"toolCall","id":"`+pending+`","name":"read","arguments":{}}],"stopReason":"toolUse","timestamp":`+s+`}`, "", 0)
				case op == 6:
					r.entry(2, "pi.system", `{"role":"system","content":"system `+s+`","timestamp":`+s+`}`, "", 0)
				case op == 7 && len(users) > 0:
					keep := users[rnd.IntN(len(users))]
					marker = r.entry(2, "pi.compaction", `{"role":"user","content":[{"type":"text","text":"summary `+s+`"}],"timestamp":`+s+`}`, `,"head":`+strconv.FormatInt(keep, 10), keep)
				case op == 8:
					if _, err := r.h.answerContext(store.TextContextRelease, []abi.Value{abi.Int(2)}); err != nil {
						t.Fatal(err)
					}
				default:
					users = append(users, r.turn(2, step))
				}
				tail := uint32(r.next - 1)
				rng, err := r.h.rangeContext(2, uint32(marker), tail)
				if err != nil {
					t.Fatalf("step %d: %v", step, err)
				}
				fresh := &Host{opts: r.h.opts, ctx: r.h.ctx}
				want, err := fresh.rangeContext(2, uint32(marker), tail)
				if err != nil {
					t.Fatal(err)
				}
				if len(rng.Msgs) != len(want.Msgs) || len(rng.Idents) != len(rng.Msgs) || len(rng.Stored) != len(rng.Msgs) {
					t.Fatalf("step %d: %d messages, %d identities, %d stored flags; a fresh host derives %d", step, len(rng.Msgs), len(rng.Idents), len(rng.Stored), len(want.Msgs))
				}
				view, err := r.h.contextView(2, int64(tail))
				if err != nil {
					t.Fatal(err)
				}
				for i := range want.Msgs {
					if !bytes.Equal(rng.Msgs[i], want.Msgs[i]) || rng.Stored[i] != want.Stored[i] {
						t.Fatalf("step %d message %d:\n got %s (stored %v)\nwant %s (stored %v)", step, i, rng.Msgs[i], rng.Stored[i], want.Msgs[i], want.Stored[i])
					}
					if id, ok := view.MessageIdent(i); ok != rng.Stored[i] || (ok && id != rng.Idents[i]) {
						t.Fatalf("step %d message %d: identity %v %v, the store's view says %v %v", step, i, rng.Idents[i], rng.Stored[i], id, ok)
					}
				}
				if len(held) > 0 {
					if last := held[len(held)-1]; last.rng.Lineage == rng.Lineage {
						for i := range min(len(last.msgs), len(rng.Msgs)) {
							if last.msgs[i] != string(rng.Msgs[i]) || last.idents[i] != rng.Idents[i] || last.stored[i] != rng.Stored[i] {
								t.Fatalf("step %d: lineage %d disagrees with the last range at message %d", step, rng.Lineage, i)
							}
						}
					}
				}
				held = append(held, holdRange(rng))
				for j, h := range held {
					for i := range h.msgs {
						if string(h.rng.Msgs[i]) != h.msgs[i] || h.rng.Idents[i] != h.idents[i] || h.rng.Stored[i] != h.stored[i] {
							t.Fatalf("step %d: range %d changed at message %d after the host went on", step, j, i)
						}
					}
				}
			}
		})
	}
}

// TestRangeContextDoesNotLookAtTheSettledPrefixAgain checks the other half of the O(new) cost: a range of a conversation
// whose derivation kept its settled lineage takes that prefix of the last range without comparing identities, so a request
// after one more turn reads the open part and the new messages, not the history. The test spoils the kept identities of the
// settled prefix: a host that compared them would see a change at message 0 and start a new lineage over new arrays.
func TestRangeContextDoesNotLookAtTheSettledPrefixAgain(t *testing.T) {
	r := newRangeDB(t)
	r.conversation(2, "")
	for i := range 40 {
		r.turn(2, i)
	}
	first, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	s := r.h.sent[2]
	if s == nil || s.nSet < 100 {
		t.Fatalf("the host kept no settled messages for the conversation: %+v", s)
	}
	for i := range s.nSet {
		s.stored[i] = false
	}
	r.turn(2, 40)
	next, err := r.h.rangeContext(2, 0, uint32(r.next-1))
	if err != nil {
		t.Fatal(err)
	}
	if next.Lineage != first.Lineage || len(next.Msgs) != len(first.Msgs)+4 {
		t.Fatalf("after one more turn the range has lineage %d and %d messages; the last had %d and %d: the settled prefix was compared again", next.Lineage, len(next.Msgs), first.Lineage, len(first.Msgs))
	}
	// A release drops what the host kept for the conversation, as it drops the store's derivation (Pi's scheduler drops the
	// kept context range of an idle conversation).
	if _, err := r.h.answerContext(store.TextContextRelease, []abi.Value{abi.Int(2)}); err != nil {
		t.Fatal(err)
	}
	if r.h.sent[2] != nil {
		t.Fatal("a released conversation's last range is still held")
	}
}
