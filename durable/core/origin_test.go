// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

func originTestHandler() *handler {
	h := &handler{b: abi.NewBuilder(), loop: &session.Loop{}}
	h.b.Origin = h.origin
	h.loop.Retain, h.loop.Release = h.holdOrigin, h.dropOrigin
	return h
}

// freedOrigins is the origins of the step's origin_free notice, and whether it has one.
func freedOrigins(h *handler) ([]uint32, bool) {
	for _, n := range h.b.Step().Notices {
		if n.Kind != abi.NoticeOriginFree {
			continue
		}
		var out []uint32
		for i := 0; i < int(binary.LittleEndian.Uint32(n.Payload)); i++ {
			out = append(out, binary.LittleEndian.Uint32(n.Payload[4+4*i:]))
		}
		return out, true
	}
	return nil, false
}

// An effect leaves stamped with the origin of the code that started it, its completion continues in that origin whoever delivers it,
// and the origin goes to the host once nothing holds it (ABI section 5.4).
func TestEffectCompletionContinuesInTheOriginOfItsStart(t *testing.T) {
	h := originTestHandler()
	// A host call in origin 5 starts the effect.
	h.holdOrigin(5)
	h.setOrigin(5)
	var ranIn, deferredIn uint32
	id := h.startEffect(abi.EffectHook, jv.Append(nil, jv.NewObject()), &pending{done: func(uint8, []byte) {
		ranIn = h.origin()
		h.loop.Defer(func() { deferredIn = h.origin() })
	}})
	if got := h.b.Step().Effects[0]; got.Kind != abi.EffectHook || got.Origin != 5 {
		t.Fatalf("the effect leaves as %+v, want a hook with origin 5", got)
	}
	h.setOrigin(0)
	h.dropOrigin(5) // the call's step ends
	h.FlushOrigins()
	if freed, ok := freedOrigins(h); ok {
		t.Fatalf("origin 5 was freed (%v) while its effect is in flight", freed)
	}
	// Another host call in origin 8 delivers the completion.
	h.b.Reset()
	h.holdOrigin(8)
	h.setOrigin(8)
	if err := h.onCompletion(&abi.Event{Kind: abi.EventHookDone, ID: id, Payload: []byte("null")}); err != nil {
		t.Fatal(err)
	}
	if err := h.loop.Run(); err != nil {
		t.Fatal(err)
	}
	if ranIn != 5 || deferredIn != 5 {
		t.Fatalf("the continuation ran in origin %d and its microtask in %d, want 5, the origin of the effect's start", ranIn, deferredIn)
	}
	h.dropOrigin(8)
	h.FlushOrigins()
	if freed, _ := freedOrigins(h); !slices.Equal(freed, []uint32{5, 8}) && !slices.Equal(freed, []uint32{8, 5}) {
		t.Fatalf("freed origins = %v, want 5 and 8", freed)
	}
	if len(h.refs.n) != 0 {
		t.Fatalf("holds left over: %v", h.refs.n)
	}
}

// A hops chain carries no origin to the host: the core keeps the origin of the work parked on it and runs it in that origin when
// the host answers hopped.
func TestHopsChainContinuesInTheOriginOfItsPark(t *testing.T) {
	h := originTestHandler()
	h.holdOrigin(6)
	h.setOrigin(6)
	var ranIn uint32
	h.holdFor(2, func() { ranIn = h.origin() })
	var id uint32
	for _, e := range h.b.Step().Effects {
		if e.Kind == abi.EffectHops {
			id = e.ID
			if e.Origin != 0 {
				t.Fatalf("the hops effect leaves with origin %d, want none", e.Origin)
			}
		}
	}
	h.setOrigin(0)
	h.dropOrigin(6)
	h.FlushOrigins()
	if _, ok := freedOrigins(h); ok {
		t.Fatal("origin 6 was freed while work is parked on its chain")
	}
	h.b.Reset()
	h.setOrigin(0)
	if err := h.onHopped(&abi.Event{Kind: abi.EventHopped, ID: id}); err != nil {
		t.Fatal(err)
	}
	if ranIn != 6 {
		t.Fatalf("the parked work ran in origin %d, want 6", ranIn)
	}
	h.FlushOrigins()
	if freed, _ := freedOrigins(h); !slices.Equal(freed, []uint32{6}) {
		t.Fatalf("freed origins = %v, want 6", freed)
	}
}

// A line job holds its origin from its queuing to the end of its run, and a parked Loop's queue holds the origins it keeps.
func TestQueuedLineJobHoldsItsOrigin(t *testing.T) {
	h := originTestHandler()
	h.holdOrigin(3)
	h.setOrigin(3)
	var ranIn uint32
	h.loop.Line(func() error { ranIn = h.origin(); return nil }, nil)
	h.setOrigin(0)
	h.dropOrigin(3)
	h.FlushOrigins()
	if _, ok := freedOrigins(h); ok {
		t.Fatal("origin 3 was freed while a line job queued in it waits")
	}
	if err := h.loop.Run(); err != nil {
		t.Fatal(err)
	}
	if ranIn != 3 {
		t.Fatalf("the job ran in origin %d, want 3", ranIn)
	}
	h.FlushOrigins()
	if freed, _ := freedOrigins(h); !slices.Equal(freed, []uint32{3}) {
		t.Fatalf("freed origins = %v, want 3", freed)
	}
}
