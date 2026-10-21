// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// scheduler.ts:1188-1195 builds the runtime's snapshotAsOf (the DocumentReader of types.ts:886) through #read, which throws endedError once the invocation has
// ended (:1282-1285): inside the phase the read returns the document as of the visible entry, afterwards it rejects with "invocation has ended" even for a
// valid read (an entry that exists), so the rejection comes from the ended check and not from a failed read.
// mutation-checked: removing the invocation check from taskRuntime.SnapshotAsOfErased (driver/taskruntime.go) lets the late read succeed.
func TestCoreTaskRuntimeSnapshotAsOfRejectsAfterTheInvocationEnds(t *testing.T) {
	notes := durable.DefineDoc(durable.DocDefinition[runtimeNotes]{
		CommonDocDefinition: durable.CommonDocDefinition[runtimeNotes]{Kind: "test.asof-after-end", Version: 1, Initial: func() runtimeNotes { return runtimeNotes{} }, CheckpointWhen: func(runtimeNotes, []durable.Op, durable.CheckpointInfo) bool { return false }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
	})
	captured := make(chan stepRuntime, 1)
	first := make(chan durable.EntryId, 1)
	inPhase := make(chan string, 1)
	task := oneStepTask("test.asof-after-end", func(ctx context.Context, rt stepRuntime) error {
		captured <- rt
		view, err := rt.Context(ctx, rt.ConversationId(), nil)
		if err != nil {
			return err
		}
		first <- view.Entries[0].Id
		asOf, err := durable.SnapshotAsOf[runtimeNotes](ctx, rt, notes, view.Entries[0].Id, rt.ConversationId())
		if err != nil {
			return err
		}
		inPhase <- asOf.Text
		return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith(nil), nil })
	})
	h, root := openTasksHarness(t, task.Erased())
	for _, text := range []string{"one", "two"} {
		if _, err := root.Commit(t.Context(), func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc[runtimeNotes](tx, notes, root.Id())
			if err != nil {
				return nil, err
			}
			if err := draft.Set("text", text); err != nil {
				return nil, err
			}
			_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}}})
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	waitSettled(t, h, startTask(t, root, task.Erased()))
	if got := <-inPhase; got != "one" {
		t.Fatalf("the read inside the phase returned %q, want the document as of the first entry (one)", got)
	}
	rt := <-captured
	// The end marks the invocation ended before it cancels the runtime signal.
	<-rt.Signal().Done()
	if _, err := rt.SnapshotAsOfErased(context.Background(), notes, <-first, root.Id()); err == nil || !strings.Contains(err.Error(), "invocation has ended") {
		t.Fatalf("snapshotAsOf after the end = %v, want the ended error", err)
	}
}
