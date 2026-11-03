// A followed Harness on a Chord host: the calls one Durable commit makes to two followed sources reach a derive over both as one
// batch (Pi follow.ts: "calls outside one are grouped into one batch on the next microtask, so a derive over two views of one
// commit computes once with both changes"). Pi's Durable publishes a commit to every observer in one synchronous step of the
// host's one thread; Go's Durable publishes from its Session writer goroutine and opens a step on the publication's context
// (chordctx.WithStep) that the followed sources of a host leave their calls in, whatever the storage: memory, sqlite and
// jsonl have no publication scope (Pi's `Storage.publishing`, StoreStorage's alone), and the one commit still reaches the
// derive as one batch.

package harness

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	jsonlnode "github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/storage/store"
)

var rrBoth = rrMust(contracts.Local("app.both", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
		contracts.Mem("both", contracts.NewMethod()),
		contracts.Mem("pair", contracts.Str()),
	},
}))

func rrFollowAttach[T any](harness chordhost.Native[any], observe func(opened Harness, ctx context.Context, observer chordhost.FollowObserver[any]) (durable.Observation[T], error)) chordhost.FollowAttach[delta.JsonValue] {
	return func(observer chordhost.FollowObserver[any], ctx context.Context) (*chordhost.FollowAttachment[delta.JsonValue], error) {
		opened, err := harness.Value()
		if err != nil {
			return nil, err
		}
		observation, err := observe(opened.(Harness), ctx, observer)
		if err != nil {
			return nil, err
		}
		value, err := durable.ToJsonValue(observation.Value())
		if err != nil {
			return nil, err
		}
		return &chordhost.FollowAttachment[delta.JsonValue]{Value: value, Detach: observation.Detach}, nil
	}
}

// rrBatchStorage opens the storage of one case; chordStore is the store a StoreStorage lives in, attached to the host.
type rrBatchStorage struct {
	name  string
	store func(t *testing.T) chordstore.Store
	open  func(t *testing.T, ctx context.Context, store chordstore.Store) (durable.Storage, error)
}

var rrBatchStorages = []rrBatchStorage{
	{name: "memory", open: func(*testing.T, context.Context, chordstore.Store) (durable.Storage, error) {
		return storage.NewMemoryStorage(), nil
	}},
	{name: "sqlite", open: func(t *testing.T, _ context.Context, _ chordstore.Store) (durable.Storage, error) {
		return sqlitenode.OpenNodeSqliteStorage(filepath.Join(t.TempDir(), "durable.sqlite"), sqlitenode.NodeSqliteStorageOptions{})
	}},
	{name: "jsonl", open: func(t *testing.T, ctx context.Context, _ chordstore.Store) (durable.Storage, error) {
		directory := t.TempDir()
		return jsonl.Open(ctx, directory, &jsonlnode.NodeFileSystem{Cwd: directory}, jsonl.JsonlStorageOptions{})
	}},
	{name: "store", store: func(t *testing.T) chordstore.Store {
		db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := chordstore.OpenStore(chordstore.SqliteBackend(db), testContext, chordstore.StoreOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = opened.Close(testContext) })
		return opened
	}, open: func(_ *testing.T, ctx context.Context, opened chordstore.Store) (durable.Storage, error) {
		opened2, err := store.Open(ctx, opened, "harness")
		if err != nil {
			return nil, err
		}
		return opened2, nil
	}},
}

func TestOneCommitChangingTwoFollowedViewsReachesADeriveAsOneBatch(t *testing.T) {
	for _, variant := range rrBatchStorages {
		t.Run(variant.name, func(t *testing.T) { rrOneCommitTwoViews(t, variant) })
	}
}

func rrOneCommitTwoViews(t *testing.T, variant rrBatchStorage) {
	done := rrDoneTask()
	var chosen chordstore.Store
	options := chordhost.HostOptions{OnError: func(chordhost.HostError) {}}
	if variant.store != nil {
		chosen = variant.store(t)
		options.Store = chosen
	}
	host, err := chordhost.CreateHost(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rrBounded(t, "closing the host", func() { _ = host.Close(testContext) }) })
	var mu sync.Mutex
	var computed []string
	rrLoad(t, host, func(facetHost chordhost.FacetHost, _ chordhost.FacetSelf) error {
		return facetHost.Serve(rrBoth, func(_ *delta.JsonObject, self chordhost.Self) (contracts.Implementation, error) {
			harness := self.Open(
				func(ctx context.Context) (any, error) {
					registry := CreateRegistry()
					if err := registry.Install(&durable.Extension{Name: "app.ext", Tasks: []durable.AnyTask{done.Erased()}}); err != nil {
						return nil, err
					}
					backing, err := variant.open(t, ctx, chosen)
					if err != nil {
						return nil, err
					}
					opened, err := OpenHarness(ctx, backing, HarnessOptions{Models: ai.CreateModels(), Registry: registry})
					if err != nil {
						return nil, err
					}
					if _, err := opened.Root(ctx, nil); err != nil {
						_ = opened.Close(ctx)
						return nil, err
					}
					return opened, nil
				},
				func(value any, _ chordhost.NativeCloseReason, ctx context.Context) error {
					return value.(Harness).Close(ctx)
				},
			)
			view := self.Follow(rrFollowAttach(harness, func(opened Harness, ctx context.Context, observer chordhost.FollowObserver[any]) (durable.Observation[ConversationView], error) {
				return opened.ObserveConversation(ctx, durable.ROOT_CONVERSATION_ID, rrViewFollower{observer}, nil)
			}))
			graph := self.Follow(rrFollowAttach(harness, func(opened Harness, ctx context.Context, observer chordhost.FollowObserver[any]) (durable.Observation[TaskGraph], error) {
				return opened.ObserveTaskGraph(ctx, rrGraphFollower{observer})
			}))
			pair := self.Derive([]any{view, graph}, func(values []any) (delta.JsonValue, error) {
				entries, _ := values[0].(*delta.JsonObject).Value("entries").([]any)
				tasks, _ := values[1].(*delta.JsonObject).Value("tasks").(*delta.JsonObject)
				n := 0
				if tasks != nil {
					n = tasks.Len()
				}
				out := fmt.Sprintf("%d,%d", len(entries), n)
				mu.Lock()
				computed = append(computed, out)
				mu.Unlock()
				return out, nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"pair": pair},
				Methods: map[string]contracts.MethodFunction{
					"both": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						chordctx.Suspend(ctx)
						opened, err := harness.Value()
						if err != nil {
							return nil, err
						}
						root, err := opened.(Harness).Conversation(ctx, durable.ROOT_CONVERSATION_ID)
						if err != nil {
							return nil, err
						}
						// One commit: a conversation entry, which the view shows, and a task, which the graph shows.
						_, err = durable.Commit(ctx, root, func(tx durable.Tx) (durable.TaskId, error) {
							if _, err := tx.AppendEntry(durable.ROOT_CONVERSATION_ID, durable.EntryDraft{Kind: "note", Data: map[string]any{}}); err != nil {
								return 0, err
							}
							return tx.CreateTaskErased(done.Erased(), nil, durable.TaskOptions{Ownership: conversationOwned})
						})
						return nil, err
					},
				},
			}}, nil
		}, chordhost.ServeOptions{})
	})
	reference := rrHeld(t, host, rrBoth)
	rrWait(t, "the object is live", func() bool { return reference.State().Value().Kind == chordhost.RefStateLive })
	read := func() string {
		value, err := reference.Member("pair").Value()
		if err != nil {
			return ""
		}
		text, _ := value.(string)
		return text
	}
	rrWait(t, "the pair is readable", func() bool { return read() != "" })
	watch, err := reference.Member("pair").Watch(testContext)
	if err != nil {
		t.Fatal(err)
	}
	var frames []any
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		mu.Lock()
		frames = append(frames, call.Value)
		mu.Unlock()
		return nil
	})
	start := read()
	mu.Lock()
	computesBefore, framesBefore := len(computed), len(frames)
	mu.Unlock()
	rrBounded(t, "the call", func() {
		if _, err := reference.Call("both", nil, testContext); err != nil {
			t.Error(err)
		}
	})
	rrWait(t, "both changes are shown", func() bool { return read() != start })
	// The frame is delivered after the commit's publication: wait for the pair to settle on both changes.
	rrWait(t, "the frame of the commit is delivered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(frames) > framesBefore
	})
	mu.Lock()
	after := append([]string(nil), computed[computesBefore:]...)
	got := append([]any(nil), frames[framesBefore:]...)
	mu.Unlock()
	if len(after) != 1 || len(got) != 1 || got[0] != "1,1" {
		t.Fatalf("from %s one commit made computes %v and frames %v; want one compute and one frame, both changes (1,1) in one batch", start, after, got)
	}
}
