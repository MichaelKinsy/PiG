// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// A model request is built from the context the host derives from its stored entries (ABI section 4.2, context reads): the core
// names the context by its newest entry and head marker and reads none of the transcript. A core that opens a long history and
// runs one turn therefore reads a few rows through pig.read, not every entry the request covers. The history here is 120 turns,
// 3 entries each at least; the core read all of them for the first request before the host built the context.
func TestCoreARequestOnALongHistoryReadsNoTranscriptThroughTheCore(t *testing.T) {
	const turns = 120
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	steps := make([]ai.FauxResponseStep, 0, turns+1)
	for range turns + 1 {
		steps = append(steps, ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}, StopReason: "stop"}))
	}
	faux.SetResponses(steps)
	options := harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry()}
	path := filepath.Join(t.TempDir(), "store.db")
	model := &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}}
	turn := func(h harness.Harness, i int) {
		t.Helper()
		root, err := h.Root(t.Context(), model)
		if err != nil {
			t.Fatal(err)
		}
		sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("turn")})
		if err != nil {
			t.Fatal(err)
		}
		if settled, err := sub.Wait(t.Context()); err != nil || settled.Status != "done" {
			t.Fatalf("turn %d: %+v %v", i, settled, err)
		}
	}
	first, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	for i := range turns {
		turn(first, i)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	again, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close(context.Background()) })
	before := again.(*driver.Session).HostCounters()
	turn(again, turns)
	after := again.(*driver.Session).HostCounters()
	rows := after.RowsRead - before.RowsRead
	if rows > 100 {
		t.Fatalf("the first turn on a %d-turn history read %d rows through the core; the host builds the context, so the core reads a few", turns, rows)
	}
	t.Logf("rows read through the core by the first turn after reopen: %d", rows)
}
