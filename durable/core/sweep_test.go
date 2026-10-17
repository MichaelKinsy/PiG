// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// A Session-mode core has no Loop: its document sweeps wait for the end of the step and for the open host transaction, and a host
// that replicates documentCacheSizes (session.ts, a synchronous getter) is told the sizes once per change.
func TestASessionModeSweepWaitsForTheHostTransaction(t *testing.T) {
	h := &handler{b: abi.NewBuilder(), mode: "session"}
	ran := 0
	h.holder = 7
	h.enqueueSweep(func() { ran++ })
	h.sweepMicrotask(func() { ran += 10 })
	h.runSessionSweeps()
	if ran != 0 {
		t.Fatalf("a sweep ran while a host transaction held the line: %d", ran)
	}
	h.holder = 0
	h.runSessionSweeps()
	if ran != 11 || len(h.sweeps) != 0 {
		t.Fatalf("sweeps ran %d, queued %d, want both run in order", ran, len(h.sweeps))
	}
}

func TestASessionModeCorePublishesTheDocumentCacheSizesWhenTheyChange(t *testing.T) {
	store := sessiontest.New()
	h := &handler{b: abi.NewBuilder(), mode: "session"}
	h.sess = session.New(store, store.NextID(), func() float64 { return 0 })
	notices := func() []abi.Notice { return append([]abi.Notice(nil), h.b.Step().Notices...) }
	h.publishDocCache()
	got := notices()
	if len(got) != 1 || got[0].Kind != abi.NoticeDocCache || string(got[0].Payload) != `{"cached":0,"idle":0,"attached":0,"sweeps":0}` {
		t.Fatalf("first notice %+v", got)
	}
	h.publishDocCache()
	if len(notices()) != 1 {
		t.Fatalf("an unchanged size was published again")
	}
	h.sess.SweepJobs = 2
	h.publishDocCache()
	got = notices()
	if len(got) != 2 || string(got[1].Payload) != `{"cached":0,"idle":0,"attached":0,"sweeps":2}` {
		t.Fatalf("after a sweep job: %+v", got)
	}
	harness := &handler{b: abi.NewBuilder(), mode: "harness", sess: h.sess}
	harness.publishDocCache()
	if len(harness.b.Step().Notices) != 1 {
		t.Fatal("a Harness-mode core did not publish the document cache sizes: Harness.documentCacheSizes needs them")
	}
}
