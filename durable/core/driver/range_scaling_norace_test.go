// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build !race

package driver

import (
	"runtime"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// growingRange is a host's range arrays for one conversation, extended in place as sqlhost.rangeContext extends them.
type growingRange struct {
	msgs   [][]byte
	idents []history.MessageIdent
	stored []bool
}

func (g *growingRange) grow(n int) *sqlhost.MessageRange {
	for range n {
		i := len(g.msgs)
		g.msgs = append(g.msgs, []byte(`{"role":"user","content":"message `+strconv.Itoa(i)+`","timestamp":1}`))
		g.idents = append(g.idents, history.MessageIdent{})
		g.stored = append(g.stored, true)
	}
	k := len(g.msgs)
	return &sqlhost.MessageRange{Conv: 1, Lineage: 1, Msgs: g.msgs[:k:k], Idents: g.idents[:k:k], Stored: g.stored[:k:k]}
}

// rangeRequestBytes is the bytes a model request allocates for its message list after 4 new messages, at a history of h
// messages: the least of three runs of 8 requests. The messages are decoded once either way; what remains is the list.
func rangeRequestBytes(t *testing.T, h int) uint64 {
	t.Helper()
	m := NewModels(nil, func() float64 { return 0 }, nil)
	g := &growingRange{}
	if _, err := m.rangeMessages(1, g.grow(h)); err != nil {
		t.Fatal(err)
	}
	best := ^uint64(0)
	for range 3 {
		var total uint64
		for call := range 8 {
			rng := g.grow(4)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if _, err := m.rangeMessages(uint32(call+2), rng); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			total += after.TotalAlloc - before.TotalAlloc
		}
		best = min(best, total/8)
	}
	return best
}

// TestRangeMessagesCostDoesNotGrowWithTheHistory checks that a model request whose range extends the conversation's last one
// costs the new messages, not the history: the list is extended, not rebuilt per request (complexity audit, Durable warm
// turn: 9 requests a turn each rebuilt a list of every message).
func TestRangeMessagesCostDoesNotGrowWithTheHistory(t *testing.T) {
	small, large := rangeRequestBytes(t, 1000), rangeRequestBytes(t, 10000)
	t.Logf("bytes per request after 4 new messages: H=1000 %d, H=10000 %d", small, large)
	if large > 2*small+4096 {
		t.Fatalf("a request after 4 new messages allocates %d bytes at 10,000 messages and %d at 1,000: it grows with the history", large, small)
	}
}
