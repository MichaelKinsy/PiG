// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// submissions.ts InputEntry through the real core: an input is placed as its own entry with the user message, and a kind the
// check rejects fails the submit with Pi's TypeError message.
func TestCoreSubmissionsPlaceAnInputAsItsInputEntryAndRejectAReservedKind(t *testing.T) {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}})})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry(), Now: func() float64 { return 42 }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	h.Resume()
	author := map[string]any{"author": "ada"}
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), InputEntry: &durable.InputEntry{Kind: "note", Data: author}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := sub.Wait(t.Context())
	if err != nil || record.Entry == nil {
		t.Fatalf("record = %+v, %v", record, err)
	}
	entry, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(*record.Entry) })
	if err != nil || entry == nil {
		t.Fatalf("entry = %v, %v", entry, err)
	}
	data, _ := json.Marshal(entry.Data)
	if entry.Kind != "note" || string(data) != `{"author":"ada"}` || len(entry.Model) != 1 || !reflect.DeepEqual(entry.Model[0], ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 42}) {
		t.Fatalf("entry = %#v", entry)
	}
	for _, kind := range []string{"pi.user", ""} {
		_, err = root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("no"), InputEntry: &durable.InputEntry{Kind: kind}})
		want := `An input entry's kind must be a non-empty string not starting with "pi.", got ` + kind
		if err == nil || err.Error() != want {
			t.Fatalf("kind %q: error = %v, want %q", kind, err, want)
		}
	}
}
