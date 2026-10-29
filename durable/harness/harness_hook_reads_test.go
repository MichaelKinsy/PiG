// Ports packages/durable/test/harness-hook-reads.test.ts (Pi 65fba65b, #11141): entry reads for hooks and tasks. See
// harness_tasks_test.go for the Go mappings that apply to every case. `api.entry(token, id)` is `token.As(api.Entry(id))`
// in Go.

package harness

// pi: packages/durable/src/harness/scheduler.ts

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func TestEntryReadsForHooksAndTasks(t *testing.T) {
	// #11141
	t.Run("reads raw history and single entries from a hook", func(t *testing.T) {
		setup := chatSetup(t)
		type seenRead struct {
			kinds []string
			user  string
		}
		var seen syncList[seenRead]
		addHooks(t, setup.Registry, GenerationTask.Erased(), &GenerationHooks{OnYield: func(ctx context.Context, _ ai.AssistantMessage, api HookApi) (*YieldContinue, error) {
			page, err := api.Entries(ctx, durable.EntryQuery{Order: new(durable.ScanAscending)}, 10, nil)
			if err != nil {
				return nil, err
			}
			first := page.Items[0]
			entry, err := api.Entry(ctx, first.Id)
			if err != nil {
				return nil, err
			}
			user, err := durable.UserEntry.As(entry)
			if err != nil {
				return nil, err
			}
			read := seenRead{kinds: entryKinds(page.Items)}
			if user != nil {
				read.user = user.Kind
			}
			seen.add(read)
			// Another kind: nil.
			other, err := durable.AssistantEntry.As(entry)
			if err != nil {
				return nil, err
			}
			if other != nil {
				t.Errorf("an assistant read of a user entry = %+v, want nil", other)
			}
			return nil, nil
		}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(ownSubmit(t, root, ownInput("go")).Wait(testContext))
		// The answer is not committed yet when onYield asks.
		got := seen.all()
		if len(got) != 1 || !slices.Equal(got[0].kinds, []string{"pi.user"}) || got[0].user != "pi.user" {
			t.Fatalf("seen = %+v, want one read of [pi.user] with a pi.user entry", got)
		}
		closeHarness(t, harness)
	})

	// #11141
	t.Run("pages a task's conversation history by entry ID range", func(t *testing.T) {
		setup := chatSetup(t)
		var pages syncList[[]durable.EntryId]
		idsOf := func(items []durable.EntryRecord) []durable.EntryId {
			ids := []durable.EntryId{}
			for _, entry := range items {
				ids = append(ids, entry.Id)
			}
			return ids
		}
		reader := tkOneStep("test.reader", func(ctx context.Context, _ stepRecordOf[durable.JsonValue], runtime stepRuntimeOf[durable.JsonValue]) error {
			ascending := durable.EntryQuery{Order: new(durable.ScanAscending)}
			all, err := runtime.Entries(ctx, ascending, 1, nil)
			if err != nil {
				return err
			}
			pages.add(idsOf(all.Items))
			rest, err := runtime.Entries(ctx, ascending, 1, *all.Next)
			if err != nil {
				return err
			}
			pages.add(idsOf(rest.Items))
			bounded, err := runtime.Entries(ctx, durable.EntryQuery{MinEntryId: &rest.Items[0].Id, MaxEntryId: &rest.Items[0].Id}, 10, nil)
			if err != nil {
				return err
			}
			pages.add(idsOf(bounded.Items))
			return runtime.Commit(ctx, func(durable.Tx, stepRecordOf[durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return completed[stepState, durable.JsonValue](nil), nil
			})
		})
		mustInstall(t, setup.Registry, new(durable.Extension{Name: "reader", Tasks: []durable.AnyTask{reader.Erased()}}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		must(ownSubmit(t, root, ownInput("go")).Wait(testContext))
		id := tkStart(t, root, reader.Erased())
		must(harness.WaitForTask(testContext, id))
		entries := allEntries(t, root)
		expectKinds(t, entries, "pi.user", "pi.assistant")
		want := [][]durable.EntryId{{entries[0].Id}, {entries[1].Id}, {entries[1].Id}}
		if got := pages.all(); !slices.EqualFunc(got, want, slices.Equal) {
			t.Fatalf("pages = %v, want %v", got, want)
		}
		closeHarness(t, harness)
	})
}
