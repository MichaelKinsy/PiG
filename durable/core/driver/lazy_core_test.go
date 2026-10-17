// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// A reopened native Harness opens with the live tasks only and reads a terminal task or an entry when a call needs it (ABI section
// 4.2, pig.read; durable/core/mirror_lazy.go): getTask, waitForTask and a task's outcome after reopen answer what they
// answered before the close, as Pi's Storage reads the settled row then.
func TestCoreReopenReadsSettledRecordsOnDemand(t *testing.T) {
	done := defineSteps("test.done", nil, map[string]func(context.Context, stepRecord, stepRuntime) error{
		"start": func(ctx context.Context, _ stepRecord, rt stepRuntime) error {
			return rt.Commit(ctx, func(durable.Tx, stepRecord) (*stepNext, error) { return completedWith("finished"), nil })
		},
	})
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{done.Erased()}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "store.db")
	options := harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry}
	h, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []durable.TaskId
	before := map[durable.TaskId]string{}
	for range 3 {
		id := startTask(t, root, done.Erased())
		waitSettled(t, h, id)
		rec, err := h.GetTask(t.Context(), id)
		if err != nil || rec == nil {
			t.Fatalf("getTask %d before the close: %v %v", id, rec, err)
		}
		ids = append(ids, id)
		before[id] = recordJSON(t, rec)
	}
	entriesBefore := readEntries(t, root)
	if !strings.Contains(entriesBefore, `"in the fork"`) || strings.Count(entriesBefore, `"kind":"note"`) < 12 || strings.Contains(entriesBefore, "head marker null") {
		t.Fatalf("the reads before the close miss the written entries:\n%s", entriesBefore)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	again, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = again.Close(context.Background()) })
	for _, id := range ids {
		rec, err := again.GetTask(t.Context(), id)
		if err != nil || rec == nil {
			t.Fatalf("getTask %d after reopen: %v %v", id, rec, err)
		}
		if got := recordJSON(t, rec); got != before[id] {
			t.Fatalf("getTask %d after reopen = %s, before the close %s", id, got, before[id])
		}
		settled := waitSettled(t, again, id)
		if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted || settled.State.Outcome.Result == nil || *settled.State.Outcome.Result != "finished" {
			t.Fatalf("waitForTask %d after reopen = %+v", id, settled.State.Outcome)
		}
	}
	if rec, err := again.GetTask(t.Context(), ids[len(ids)-1]+1000); err != nil || rec != nil {
		t.Fatalf("getTask of an id storage lacks = %v, %v; want nil", rec, err)
	}
	rootAgain, err := again.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := readEntries(t, rootAgain); got != entriesBefore {
		t.Fatalf("entry reads after reopen:\n%s\nbefore the close:\n%s", got, entriesBefore)
	}
}

// readEntries appends entries to the conversation and a fork of it on the first call, and renders what the entry reads
// answer: each entry by id, both conversations' scans in both orders and pages, and their latest head markers.
func readEntries(t *testing.T, conversation harness.Conversation) string {
	t.Helper()
	out, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (string, error) {
		page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: conversation.Id()}, 0, nil)
		if err != nil {
			return "", err
		}
		return fmt.Sprint(len(page.Items)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == "0" {
		at, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (any, error) {
			var at durable.EntryRecord
			for i := range 20 { // more than the first page of a stored id scan (mirror_lazy.go entriesDesc)
				e, err := tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "note", Data: fmt.Sprint("n", i), HeadSelf: i == 2})
				if err != nil {
					return nil, err
				}
				if i == 3 {
					at = e
				}
			}
			return at.Id, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (any, error) {
			fork, err := tx.ForkConversation(conversation.Id(), at.(durable.EntryId), durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
			if err != nil {
				return nil, err
			}
			_, err = tx.AppendEntry(fork.Id, durable.EntryDraft{Kind: "note", Data: "in the fork"})
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	text, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (string, error) {
		var b strings.Builder
		convs, err := tx.ScanConversations(durable.ConversationQuery{}, 0, nil)
		if err != nil {
			return "", err
		}
		asc := durable.ScanAscending
		for _, c := range convs.Items {
			for _, order := range []*durable.ScanOrder{nil, &asc} {
				var cursor durable.Cursor
				for {
					page, err := tx.ScanEntries(durable.EntryQuery{ConversationId: c.Id, Order: order}, 2, cursor)
					if err != nil {
						return "", err
					}
					fmt.Fprintf(&b, "conv %d page %s\n", c.Id, recordJSON(t, page.Items))
					for _, e := range page.Items {
						one, err := tx.Entry(e.Id)
						if err != nil {
							return "", err
						}
						fmt.Fprintf(&b, "entry %s\n", recordJSON(t, one))
					}
					if page.Next == nil {
						break
					}
					cursor = *page.Next
				}
			}
			marker, err := tx.LatestHeadMarker(c.Id)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "conv %d head marker %s\n", c.Id, recordJSON(t, marker))
		}
		return b.String(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func recordJSON(t *testing.T, rec any) string {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
