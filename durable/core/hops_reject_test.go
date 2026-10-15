// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// A rejected step drops its effects (abi.Builder.Reject), so the hops chain it opened is never run and no hopped event answers it. The
// work parked on that chain must go with it: close waits for every parked chain (closeWhenQuiet), so a stale one hangs Harness.close
// (U-harness-scheduler-retention 'drops what a rejected commit's chain load read': a transaction operation the Session rejects).
func TestARejectedStepTakesItsHopsChainsBack(t *testing.T) {
	h := &handler{b: abi.NewBuilder()}
	released := 0
	h.stepNo++
	h.holdFor(3, func() { released++ }) // opened by an earlier step: it stays
	h.stepNo++
	h.holdFor(16, func() { released += 10 })
	h.holdFor(2, func() { released += 100 })
	if len(h.hops) != 3 {
		t.Fatalf("parked %d chains, want 3", len(h.hops))
	}
	h.dropStepHops()
	if len(h.hops) != 1 {
		t.Fatalf("after the rejected step %d chains are parked, want the earlier one only", len(h.hops))
	}
	h.hops[0].release()
	if released != 1 {
		t.Fatalf("the remaining chain releases %d, want the earlier step's work", released)
	}
}
