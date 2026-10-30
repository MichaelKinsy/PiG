// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Ports packages/durable/test/harness-read-only.test.ts (Pi be14d9732) over both engines: a read-only Harness, a viewer of a
// session another process drives, such as a synced copy on another device. The mappings of durable/harness/harness_read_only_test.go
// apply: a rejected promise is a returned error, a synchronous throw (resume()) is a panic, and expect(...).rejects.toBeInstanceOf is
// errors.AsType. Every case runs on the reference Harness and on the core, each engine on its own file.

// roChat is chat-support.ts chatSetup: the faux provider, its models, a registry and the reports the Harness made.
type roChat struct {
	faux     *ai.FauxProviderHandle
	models   *ai.Models
	registry harness.Registry
	reports  *reportList
}

func newROChat() *roChat {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return &roChat{faux: faux, models: models, registry: harness.CreateRegistry(), reports: &reportList{}}
}

// roOpen opens a Harness over path on the reference (core false) or on the core, optionally read-only.
func roOpen(t *testing.T, core bool, path string, setup *roChat, readOnly bool, onWake func(harness.Wake)) harness.Harness {
	t.Helper()
	options := harness.HarnessOptions{
		Models:   setup.models,
		Registry: setup.registry,
		ReadOnly: readOnly,
		OnReport: func(err error, _ durable.ReportInfo) { setup.reports.add(err) },
		OnWake:   onWake,
	}
	var opened harness.Harness
	var err error
	if core {
		opened, err = driver.OpenHarness(t.Context(), path, options)
	} else {
		var store durable.Storage
		if store, err = sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{}); err != nil {
			t.Fatal(err)
		}
		opened, err = harness.OpenHarness(t.Context(), store, options)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	return opened
}

// roChatRoot is chat-support.ts openChat: a writable Harness and its root, configured with the faux model.
func roChatRoot(t *testing.T, core bool, path string, setup *roChat) (harness.Harness, harness.Conversation) {
	t.Helper()
	opened := roOpen(t, core, path, setup, false, nil)
	root, err := opened.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return opened, root
}

// roHead is the next sequence the file would commit, read beside every open handle; ok is false before the schema exists.
func roHead(t *testing.T, path string) (next int64, ok bool) {
	t.Helper()
	db, err := sqlitenode.NewDatabaseSync(path, sqlitenode.DatabaseSyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	exists, err := mustPrepare(t, db, "SELECT 1 AS value FROM sqlite_master WHERE name = 'durable_metadata'").Get()
	if err != nil {
		t.Fatal(err)
	}
	if exists == nil {
		return 0, false
	}
	row, err := mustPrepare(t, db, "SELECT next_seq AS value FROM durable_metadata WHERE singleton = 1").Get()
	if err != nil {
		t.Fatal(err)
	}
	return row["value"].(int64), true
}

func mustPrepare(t *testing.T, db *sqlitenode.DatabaseSync, sql string) *sqlitenode.StatementSync {
	t.Helper()
	statement, err := db.Prepare(sql)
	if err != nil {
		t.Fatal(err)
	}
	return statement
}

func roExpectRefused(t *testing.T, what string, err error) {
	t.Helper()
	refused, ok := errors.AsType[*durable.ReadOnlySession](err)
	if !ok {
		t.Fatalf("%s = %v, want *ReadOnlySession", what, err)
	}
	if refused.Name() != "ReadOnlySession" {
		t.Fatalf("%s name = %q", what, refused.Name())
	}
}

// roRunning is a session whose root generation is running in another Harness, still open, waiting for the model.
func roRunning(t *testing.T, core bool, path string, setup *roChat) (harness.Harness, harness.Conversation) {
	t.Helper()
	reached := make(chan struct{})
	var once sync.Once
	setup.faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		<-options.Signal.Done()
		return ai.AssistantMessage{}, context.Cause(options.Signal)
	})})
	driverHarness, root := roChatRoot(t, core, path, setup)
	if _, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("slow")}); err != nil {
		t.Fatal(err)
	}
	<-reached
	return driverHarness, root
}

func roEntries(t *testing.T, conversation harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := conversation.Entries(t.Context(), durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]durable.EntryRecord, 0, len(page.Items))
	for _, entry := range slices.Backward(page.Items) {
		entries = append(entries, entry)
	}
	return entries
}

func roLive(t *testing.T, opened harness.Harness, id durable.ConversationId) *harness.LiveState {
	t.Helper()
	live, err := durable.Snapshot(t.Context(), opened, harness.LiveDoc, id)
	if err != nil {
		t.Fatal(err)
	}
	return live
}

func TestReadOnlyHarnessCore(t *testing.T) {
	for _, engine := range []struct {
		name string
		core bool
	}{{"reference", false}, {"core", true}} {
		t.Run(engine.name, func(t *testing.T) {
			core := engine.core
			// harness-read-only.test.ts it("opens a session with work running elsewhere without writing")
			t.Run("opens a session with work running elsewhere without writing", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "session.sqlite")
				driverHarness, _ := roRunning(t, core, path, newROChat())
				before, _ := roHead(t, path)
				var mu sync.Mutex
				var wakes []harness.Wake

				viewer := roOpen(t, core, path, newROChat(), true, func(wake harness.Wake) {
					mu.Lock()
					defer mu.Unlock()
					wakes = append(wakes, wake)
				})
				root, err := viewer.Root(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				entries := roEntries(t, root)
				if len(entries) != 1 || entries[0].Kind != durable.UserEntry.Kind {
					t.Fatalf("entries = %+v, want one user entry", entries)
				}
				run := roLive(t, viewer, root.Id()).Run
				if run == nil {
					t.Fatal("the live document has no run")
				}
				record, err := viewer.GetTask(t.Context(), run.TaskId)
				if err != nil {
					t.Fatal(err)
				}
				if record == nil || record.State.Status != durable.TaskRunning {
					t.Fatalf("task = %+v, want running as committed", record)
				}
				inspection, err := viewer.Inspect(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if inspection.Scheduling != "paused" {
					t.Fatalf("scheduling = %q, want paused", inspection.Scheduling)
				}
				if wake := viewer.Wake(); !reflect.DeepEqual(wake, harness.Wake{Active: false}) {
					t.Fatalf("wake = %+v, want inactive", wake)
				}
				if err := viewer.Close(t.Context()); err != nil {
					t.Fatal(err)
				}

				if after, _ := roHead(t, path); after != before {
					t.Fatalf("head = %d, want %d: the viewer wrote", after, before)
				}
				mu.Lock()
				if len(wakes) != 0 {
					t.Fatalf("wakes = %+v, want none", wakes)
				}
				mu.Unlock()
				if err := driverHarness.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			})

			// harness-read-only.test.ts it("refuses writes and scheduling, and stays usable")
			t.Run("refuses writes and scheduling, and stays usable", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "session.sqlite")
				setup := newROChat()
				setup.faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("one")}})})
				driverHarness, driverRoot := roChatRoot(t, core, path, setup)
				submission, err := driverRoot.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := submission.Wait(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := driverHarness.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				before, _ := roHead(t, path)

				viewerSetup := newROChat()
				viewer := roOpen(t, core, path, viewerSetup, true, nil)
				root, err := viewer.Root(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.EntryRecord, error) {
					return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.note"})
				})
				roExpectRefused(t, "commit with writes", err)
				_, err = root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("again")})
				roExpectRefused(t, "submit", err)
				func() {
					defer func() {
						recovered, _ := recover().(error)
						roExpectRefused(t, "resume", recovered)
					}()
					viewer.Resume()
					t.Fatal("resume() did not throw")
				}()
				roExpectRefused(t, "waitForIdle", viewer.WaitForIdle(t.Context()))
				roExpectRefused(t, "abort", root.Abort(t.Context(), nil))
				_, err = viewer.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
				roExpectRefused(t, "createConversation", err)

				// Still healthy: reads work, and a commit that writes nothing succeeds.
				nothing, err := durable.Commit(t.Context(), root, func(durable.Tx) (string, error) { return "nothing", nil })
				if err != nil || nothing != "nothing" {
					t.Fatalf("commit = %q, %v", nothing, err)
				}
				if got := len(roEntries(t, root)); got != 2 {
					t.Fatalf("entries = %d, want 2", got)
				}
				if err := viewer.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if after, _ := roHead(t, path); after != before {
					t.Fatalf("head = %d, want %d: the viewer wrote", after, before)
				}
				if reports := viewerSetup.reports.all(); len(reports) != 0 {
					t.Fatalf("reports = %v, want none", reports)
				}
			})

			// harness-read-only.test.ts it("refuses to create the root of an empty storage")
			t.Run("refuses to create the root of an empty storage", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "session.sqlite")
				viewer := roOpen(t, core, path, newROChat(), true, nil)
				_, err := viewer.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
				roExpectRefused(t, "root", err)
				if err := viewer.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if head, _ := roHead(t, path); head != 1 {
					t.Fatalf("head = %d, want 1", head)
				}
			})

			// harness-read-only.test.ts it("leaves recovery to the next writable open")
			t.Run("leaves recovery to the next writable open", func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "session.sqlite")
				driverHarness, driverRoot := roRunning(t, core, path, newROChat())
				taskID := roLive(t, driverHarness, driverRoot.Id()).Run.TaskId

				viewer := roOpen(t, core, path, newROChat(), true, nil)
				if err := viewer.Close(t.Context()); err != nil {
					t.Fatal(err)
				}

				// The writable open recovers the task once, as if the viewer had never opened it.
				writer, _ := roChatRoot(t, core, path, newROChat())
				record, err := writer.GetTask(t.Context(), taskID)
				if err != nil {
					t.Fatal(err)
				}
				if record == nil || record.State.Status != durable.TaskPending || record.Restarts != 1 {
					t.Fatalf("task = %+v, want pending after one restart", record)
				}
				if err := writer.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := driverHarness.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
