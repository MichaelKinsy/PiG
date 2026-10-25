// Ports packages/durable/test/harness-read-only.test.ts: a read-only Harness, a viewer of a session another process
// drives, such as a synced copy on another device. See harness_tasks_test.go for the Go mappings that apply to every
// case: a rejected promise is a returned error, a synchronous throw (resume()) is a panic, and expect(...).rejects.toBeInstanceOf
// is errors.AsType.

package harness

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

func openReadOnly(t *testing.T, path string, setup *chatState, onWake func(Wake)) Harness {
	t.Helper()
	harness, err := OpenHarness(testContext, openSqlite(t, path), HarnessOptions{
		Models:   setup.Models,
		Registry: setup.Registry,
		ReadOnly: true,
		OnReport: setup.Reports.add,
		OnWake:   onWake,
	})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// readOnlyHead is the next sequence the file would commit, read beside every open handle; ok is false before the schema
// exists.
func readOnlyHead(t *testing.T, path string) (next int64, ok bool) {
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
	exists := must(must(db.Prepare("SELECT 1 AS value FROM sqlite_master WHERE name = 'durable_metadata'")).Get())
	if exists == nil {
		return 0, false
	}
	row := must(must(db.Prepare("SELECT next_seq AS value FROM durable_metadata WHERE singleton = 1")).Get())
	return row["value"].(int64), true
}

func expectReadOnlySession(t *testing.T, what string, err error) {
	t.Helper()
	refused, ok := errors.AsType[*durable.ReadOnlySession](err)
	if !ok {
		t.Fatalf("%s = %v, want *ReadOnlySession", what, err)
	}
	if refused.Name() != "ReadOnlySession" {
		t.Fatalf("%s name = %q", what, refused.Name())
	}
}

func expectNoReports(t *testing.T, setup *chatState) {
	t.Helper()
	setup.Reports.mu.Lock()
	defer setup.Reports.mu.Unlock()
	if len(setup.Reports.reports) != 0 {
		t.Fatalf("reports = %v, want none", setup.Reports.reports)
	}
}

// runningSession is a session whose root generation is running in another Harness, still open, waiting for the model.
func runningSession(t *testing.T, path string, setup *chatState) (Harness, Conversation) {
	t.Helper()
	busy := unanswered()
	setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
	driver, root := openChat(t, openSqlite(t, path), setup)
	submitInput(t, root, "slow")
	<-busy.reached
	return driver, root
}

func TestReadOnlyHarness(t *testing.T) {
	// harness-read-only.test.ts it("opens a session with work running elsewhere without writing")
	t.Run("opens a session with work running elsewhere without writing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		driver, driverRoot := runningSession(t, path, setup)
		before, _ := readOnlyHead(t, path)
		var mu sync.Mutex
		var wakes []Wake

		viewer := openReadOnly(t, path, chatSetup(t), func(wake Wake) {
			mu.Lock()
			defer mu.Unlock()
			wakes = append(wakes, wake)
		})
		root := must(viewer.Root(testContext, nil))
		entries := allEntries(t, root)
		if len(entries) != 1 || entries[0].Kind != durable.UserEntry.Kind {
			t.Fatalf("entries = %+v, want one user entry", entries)
		}
		run := liveOf(t, viewer, root.Id()).Run
		if run == nil {
			t.Fatal("the live document has no run")
		}
		if record := must(viewer.GetTask(testContext, run.TaskId)); record == nil || record.State.Status != durable.TaskRunning {
			t.Fatalf("task = %+v, want running as committed", record)
		}
		if inspection := must(viewer.Inspect(testContext)); inspection.Scheduling != "paused" {
			t.Fatalf("scheduling = %q, want paused", inspection.Scheduling)
		}
		if wake := viewer.Wake(); !reflect.DeepEqual(wake, Wake{Active: false}) {
			t.Fatalf("wake = %+v, want inactive", wake)
		}
		closeHarness(t, viewer)

		if after, _ := readOnlyHead(t, path); after != before {
			t.Fatalf("head = %d, want %d: the viewer wrote", after, before)
		}
		mu.Lock()
		if len(wakes) != 0 {
			t.Fatalf("wakes = %+v, want none", wakes)
		}
		mu.Unlock()
		_ = driverRoot
		closeHarness(t, driver)
	})

	// harness-read-only.test.ts it("refuses writes and scheduling, and stays usable")
	t.Run("refuses writes and scheduling, and stays usable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("one")})
		driver, driverRoot := openChat(t, openSqlite(t, path), setup)
		must(submitInput(t, driverRoot, "hi").Wait(testContext))
		closeHarness(t, driver)
		before, _ := readOnlyHead(t, path)

		viewerSetup := chatSetup(t)
		viewer := openReadOnly(t, path, viewerSetup, nil)
		root := must(viewer.Root(testContext, nil))
		_, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "app.note"})
		})
		expectReadOnlySession(t, "commit with writes", err)
		_, err = root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("again")})
		expectReadOnlySession(t, "submit", err)
		func() {
			defer func() {
				expectReadOnlySession(t, "resume", asError(recover()))
			}()
			viewer.Resume()
			t.Fatal("resume() did not throw")
		}()
		expectReadOnlySession(t, "waitForIdle", viewer.WaitForIdle(testContext))
		expectReadOnlySession(t, "abort", root.Abort(testContext, nil))
		_, err = viewer.CreateConversation(testContext, ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}})
		expectReadOnlySession(t, "createConversation", err)

		// Still healthy: reads work, and a commit that writes nothing succeeds.
		nothing := must(durable.Commit(testContext, root, func(durable.Tx) (string, error) { return "nothing", nil }))
		if nothing != "nothing" {
			t.Fatalf("commit = %q", nothing)
		}
		if got := len(allEntries(t, root)); got != 2 {
			t.Fatalf("entries = %d, want 2", got)
		}
		closeHarness(t, viewer)
		if after, _ := readOnlyHead(t, path); after != before {
			t.Fatalf("head = %d, want %d: the viewer wrote", after, before)
		}
		expectNoReports(t, viewerSetup)
	})

	// harness-read-only.test.ts it("refuses to create the root of an empty storage")
	t.Run("refuses to create the root of an empty storage", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		viewer := openReadOnly(t, path, chatSetup(t), nil)
		_, err := viewer.Root(testContext, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
		expectReadOnlySession(t, "root", err)
		closeHarness(t, viewer)
		if head, _ := readOnlyHead(t, path); head != 1 {
			t.Fatalf("head = %d, want 1", head)
		}
	})

	// harness-read-only.test.ts it("leaves recovery to the next writable open")
	t.Run("leaves recovery to the next writable open", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		driver, driverRoot := runningSession(t, path, setup)
		taskId := liveOf(t, driver, driverRoot.Id()).Run.TaskId

		viewer := openReadOnly(t, path, chatSetup(t), nil)
		closeHarness(t, viewer)

		// The writable open recovers the task once, as if the viewer had never opened it.
		writer, _ := openChat(t, openSqlite(t, path), chatSetup(t))
		record := must(writer.GetTask(testContext, taskId))
		if record == nil || record.State.Status != durable.TaskPending || record.Restarts != 1 {
			t.Fatalf("task = %+v, want pending after one restart", record)
		}
		closeHarness(t, writer)
		closeHarness(t, driver)
	})
}

// asError is the value a recovered panic carries, as an error.
func asError(recovered any) error {
	err, _ := recovered.(error)
	return err
}
