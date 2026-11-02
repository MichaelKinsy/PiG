// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
)

// A host-driven commit answers its end hopcost.Delivery ticks before its caller would resume: the facade needs those passes from the core's
// tx_result to the caller's resumption (client.ts end, facade/tx.ts runCommit), so the caller resumes where Pi's `await commit(...)` does
// (Pi 6c384aa3 e7a0df27 F-ticks-runtime-drift; the guard of BF-228dbea, which the group commit replaced).
func TestAHostCommitIsAnsweredHostAnswerLeadTicksBeforeItsCallerResumes(t *testing.T) {
	// answerAfter is the ticks from the end of the callback to the answer,
	answerAfter := func(t *testing.T, end func(h *handler, c *hopClock) (answered func() bool)) int {
		t.Helper()
		h, c := lineRig(t, "gate-capsql")
		answered := end(h, c)
		base, after := c.now, -1
		for i := range 80 {
			c.at(base+i, func() {
				if after < 0 && answered() {
					after = i
				}
			})
		}
		c.run(t)
		if after < 0 {
			t.Fatal("the commit was never answered")
		}
		return after
	}
	production := answerAfter(t, func(h *handler, c *hopClock) func() bool {
		h.txs = map[uint32]*hostTx{}
		if err := h.txBegin(1, parsed(t, `{"conversationId":0}`)); err != nil {
			t.Fatal(err)
		}
		c.run(t)
		ht := h.txs[1]
		if ht == nil || ht.tx == nil {
			t.Fatal("the transaction never took the line")
		}
		if err := h.txEnd(ht, nil); err != nil {
			t.Fatal(err)
		}
		return func() bool { return h.txs[1] == nil }
	})
	// The same job answering at its own resumption, with no lead.
	plain := answerAfter(t, func(h *handler, c *hopClock) func() bool {
		done := false
		job := h.loop.LineHost(func() error { return nil }, func(error) { done = true })
		c.run(t)
		job.End(nil)
		return func() bool { return done }
	})
	if plain-production != hopcost.Delivery {
		t.Fatalf("the commit is answered %d ticks after its end, %d after the end of a job with no lead: want hopcost.Delivery = %d ticks earlier", production, plain, hopcost.Delivery)
	}
}
