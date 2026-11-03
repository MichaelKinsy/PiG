package store

// Ports packages/durable/test/store-storage.test.ts
//
// The cases that need a Chord host or a Harness: the commits of a Session published inside the attached host's batch,
// Durable's views as Chord sources on the store (slice 6b §3), and the GC case.

import (
	"context"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// followed adapts a Durable observer's attachment to what `self.follow` takes: Chord's observer gets the JSON of each
// value.
func followed[T any](attach func(ctx context.Context, observer durable.Observer[T]) (durable.Observation[T], error)) host.FollowAttach[delta.JsonValue] {
	return func(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
		observation, err := attach(ctx, jsonObserver[T]{observer})
		if err != nil {
			return nil, err
		}
		initial, err := durable.ToJsonValue(observation.Value())
		if err != nil {
			return nil, err
		}
		return &host.FollowAttachment[delta.JsonValue]{Value: initial, Detach: observation.Detach}, nil
	}
}

// jsonObserver is a Durable observer handing Chord's observer the JSON of each value.
type jsonObserver[T any] struct{ observer host.FollowObserver[any] }

func (each jsonObserver[T]) Advance(ctx context.Context, call chord.WatchCall[T]) {
	converted := chord.WatchCall[any]{Kind: call.Kind, Ops: call.Ops, Retired: call.Retired, Error: call.Error}
	if call.Kind == chord.WatchCallValue {
		json, err := durable.ToJsonValue(call.Value)
		if err != nil {
			panic(err)
		}
		converted.Value = json
	}
	each.observer.Advance(converted, ctx)
}

func (each jsonObserver[T]) End(end chord.WatchEnd) { each.observer.End(end) }

// openHarness is the `self.open` of a Harness over a StoreStorage of the store, its root created, then prepared.
func openHarness(store chordstore.Store, owner string, prepare func(ctx context.Context, root harness.Conversation) error) func(ctx context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		storage, err := Open(ctx, store, owner)
		if err != nil {
			return nil, err
		}
		opened, err := harness.OpenHarness(ctx, storage, harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()})
		if err != nil {
			return nil, err
		}
		root, err := opened.Root(ctx, nil)
		if err != nil {
			return nil, err
		}
		if prepare != nil {
			if err := prepare(ctx, root); err != nil {
				return nil, err
			}
		}
		return opened, nil
	}
}

func closeHarness(value any, _ host.NativeCloseReason, ctx context.Context) error {
	return value.(harness.Harness).Close(ctx)
}

var chatContract = mustContract(contracts.Local("app.chat", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("entries", contracts.Int()),
		contracts.Mem("note", contracts.NewMethod(contracts.Obj(contracts.Prop("kind", contracts.Str())))),
	},
}))

var liveContract = mustContract(contracts.Local("app.live", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int()), contracts.Mem("bump", contracts.NewMethod())},
}))

var memoDoc = func() *documents.StateDefinition {
	definition, err := documents.DefineState("memo", documents.StateOptions{
		Schema:         contracts.Obj(contracts.Prop("m", contracts.Int())),
		Version:        1,
		Initial:        func() *delta.JsonObject { return delta.JsonObjectOf("m", 1.0) },
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return true },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

var memoContract = mustContract(contracts.Local("app.memo", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("memo", contracts.Obj(contracts.Prop("m", contracts.Int()))), contracts.Mem("set", contracts.NewMethod())},
}))

// persistMemo is an object's persisted document in the store, set to 2 through a host that then closes (Pi's
// `defineState("memo")` of `app.memo`).
func persistMemo(t *testing.T, store *chordstore.ChordStore) {
	t.Helper()
	chordHost := value(host.CreateHost(host.HostOptions{Store: store, OnError: func(host.HostError) {}}))
	facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(memoContract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			memo := self.State(memoDoc).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"memo": self.Derive([]any{memo}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
				Methods: map[string]contracts.MethodFunction{
					"set": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := memo.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("m", 2.0) })
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
	value(chordHost.Load([]host.HostFacet{{Name: "app", Facet: facet}}, background))
	value(value(chordHost.Get(background, memoContract, delta.JsonObjectOf(), host.GetOptions{})).Call("set", nil, background))
	must(t, chordHost.Close(background))
}

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// collected is the calls of a watch, written from the host's goroutines.
type collected struct {
	mu    sync.Mutex
	items []any
}

func (c *collected) add(item any) {
	c.mu.Lock()
	c.items = append(c.items, item)
	c.mu.Unlock()
}

func (c *collected) all() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]any(nil), c.items...)
}

// watched starts a watch of a member of the reference, collecting each call's value (the kind of a failure).
func watched(t *testing.T, reference *host.Reference, member string) *collected {
	t.Helper()
	watch := value(reference.Member(member).Watch(background))
	seen := &collected{}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			seen.add(call.Value)
		} else {
			seen.add(string(call.Kind))
		}
		return nil
	})
	return seen
}

// eventually waits until ok holds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStoreStorageCommitsInsideTheHostsBatch(t *testing.T) {
	t.Run("publishes each commit of a Session inside the attached host's batch, and plainly without a host", func(t *testing.T) {
		store := memoryStore(t).(*chordstore.ChordStore)
		storage := value(Open(background, store, "harness"))
		var plain []bool
		storage.Publishing(background, func() { plain = append(plain, store.Batch() == nil) })
		if !reflect.DeepEqual(plain, []bool{true}) {
			t.Fatalf("plain publication = %v, want [true]", plain)
		}
		chordHost := value(host.CreateHost(host.HostOptions{Store: store, OnError: func(host.HostError) {}}))
		kernel := session.NewSessionImpl(storage, session.Hooks{}, nil, nil)
		var mu sync.Mutex
		var running []bool
		kernel.FollowCommits(func(context.Context, session.ObservedCommit) {
			mu.Lock()
			defer mu.Unlock()
			running = append(running, store.Batch().Running())
		})
		value(kernel.CommitWith(background, func(tx *session.Transaction) (any, error) { return tx.CreateRootConversation() }, session.TransactionScope{}))
		var group sync.WaitGroup
		committed := make(chan error, 2)
		for _, kind := range []string{"a", "b"} {
			group.Go(func() {
				_, err := kernel.Commit(background, func(tx durable.Tx) (any, error) {
					return tx.AppendEntry(durable.ROOT_CONVERSATION_ID, durable.EntryDraft{Kind: kind})
				})
				committed <- err
			})
		}
		group.Wait()
		close(committed)
		for err := range committed {
			must(t, err)
		}
		mu.Lock()
		got := append([]bool(nil), running...)
		mu.Unlock()
		if !reflect.DeepEqual(got, []bool{true, true, true}) {
			t.Fatalf("publication in the batch = %v, want [true true true]", got)
		}
		must(t, kernel.Close(background))
		must(t, chordHost.Close(background))
		must(t, store.Close(background))
	})
}

func TestStoreStorageViewsAsChordSources(t *testing.T) {
	t.Run("a derive over the conversation view and the task graph: one frame per Durable commit, both views in one batch", func(t *testing.T) {
		store := memoryStore(t)
		chordHost := value(host.CreateHost(host.HostOptions{Store: store, OnError: func(host.HostError) {}}))
		var computes sync.Mutex
		count := 0
		facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(chatContract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				opened := self.Open(openHarness(store, chatContract.Name, nil), closeHarness)
				current := func() harness.Harness { return value(opened.Value()).(harness.Harness) }
				view := self.Follow(followed(func(ctx context.Context, observer durable.Observer[harness.ConversationView]) (durable.Observation[harness.ConversationView], error) {
					return current().ObserveConversation(ctx, durable.ROOT_CONVERSATION_ID, observer, nil)
				}))
				graph := self.Follow(followed(func(ctx context.Context, observer durable.Observer[harness.TaskGraph]) (durable.Observation[harness.TaskGraph], error) {
					return current().ObserveTaskGraph(ctx, observer)
				}))
				entries := self.Derive([]any{view, graph}, func(values []any) (delta.JsonValue, error) {
					computes.Lock()
					count++
					computes.Unlock()
					return float64(len(values[0].(*delta.JsonObject).Value("entries").([]any))), nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"entries": entries},
					Methods: map[string]contracts.MethodFunction{
						"note": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							// The handler waits for the Harness, whose commits call the host observers from its own goroutines: it gives the
							// section back first.
							chordctx.Suspend(ctx)
							kind := args.(*delta.JsonObject).Value("kind").(string)
							root, err := current().Conversation(ctx, durable.ROOT_CONVERSATION_ID)
							if err != nil {
								return nil, err
							}
							_, err = root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: kind}})
							return nil, err
						},
					},
				}}, nil
			}, host.ServeOptions{})
		})
		value(chordHost.Load([]host.HostFacet{{Name: "app", Facet: facet}}, background))
		chat := value(chordHost.Get(background, chatContract, delta.JsonObjectOf(), host.GetOptions{}))
		seen := watched(t, chat, "entries")
		computes.Lock()
		before := count
		computes.Unlock()
		value(chat.Call("note", delta.JsonObjectOf("kind", "test.note"), background))
		entries := func() float64 { return value(chat.Member("entries").Value()).(float64) }
		eventually(t, "the frame of the note", func() bool { return len(seen.all()) > 0 })
		time.Sleep(10 * time.Millisecond)
		if entries() <= 0 {
			t.Fatalf("entries = %v, want > 0", entries())
		}
		if got := seen.all(); !reflect.DeepEqual(got, []any{entries()}) {
			t.Fatalf("calls = %v, want [%v]", got, entries())
		}
		computes.Lock()
		after := count
		computes.Unlock()
		if after-before != 1 {
			t.Fatalf("computes = %d, want 1", after-before)
		}
		must(t, chordHost.Close(background))
		must(t, store.Close(background))
	})

	t.Run("a deferred Durable commit, then an immediate one, reach the followed document in order", func(t *testing.T) {
		store := memoryStore(t)
		chordHost := value(host.CreateHost(host.HostOptions{Store: store, OnError: func(host.HostError) {}}))
		// set is a commit setting the root's LiveDoc failure note to n.
		set := func(n int) func(tx durable.Tx) (any, error) {
			return func(tx durable.Tx) (any, error) {
				draft, err := tx.Doc(harness.LiveDoc, durable.ROOT_CONVERSATION_ID)
				if err != nil {
					return nil, err
				}
				failure, err := durable.ToJsonValue(harness.CompactionFailure{TaskId: 1, Trigger: durable.CompactionReason("manual"), Error: strconv.Itoa(n)})
				if err != nil {
					return nil, err
				}
				return nil, draft.Set("compactionFailure", failure)
			}
		}
		facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
			return h.Serve(liveContract, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
				opened := self.Open(openHarness(store, liveContract.Name, func(ctx context.Context, root harness.Conversation) error {
					_, err := root.Commit(ctx, set(0))
					return err
				}), closeHarness)
				current := func() harness.Harness { return value(opened.Value()).(harness.Harness) }
				live := self.Follow(followed(func(ctx context.Context, observer durable.Observer[harness.LiveState]) (durable.Observation[harness.LiveState], error) {
					return durable.ObserveDoc(ctx, current(), harness.LiveDoc, observer, durable.ROOT_CONVERSATION_ID)
				}))
				n := self.Derive([]any{live}, func(values []any) (delta.JsonValue, error) {
					failure, ok := values[0].(*delta.JsonObject).Value("compactionFailure").(*delta.JsonObject)
					if !ok {
						return -1.0, nil
					}
					parsed, err := strconv.ParseFloat(failure.Value("error").(string), 64)
					if err != nil {
						return -1.0, nil
					}
					return parsed, nil
				})
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": n},
					Methods: map[string]contracts.MethodFunction{
						"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
							chordctx.Suspend(ctx)
							root, err := current().Conversation(ctx, durable.ROOT_CONVERSATION_ID)
							if err != nil {
								return nil, err
							}
							within := 10000.0
							if _, err := root.Commit(ctx, set(1), durable.CommitOptions{Persistence: &durable.CommitPersistence{Kind: durable.CommitDeferred, WithinMs: &within}}); err != nil {
								return nil, err
							}
							_, err = root.Commit(ctx, set(2))
							return nil, err
						},
					},
				}}, nil
			}, host.ServeOptions{})
		})
		value(chordHost.Load([]host.HostFacet{{Name: "app", Facet: facet}}, background))
		live := value(chordHost.Get(background, liveContract, delta.JsonObjectOf(), host.GetOptions{}))
		seen := watched(t, live, "n")
		value(live.Call("bump", nil, background))
		time.Sleep(20 * time.Millisecond)
		if got := value(live.Member("n").Value()); got != 2.0 {
			t.Fatalf("n = %v, want 2", got)
		}
		if all := seen.all(); len(all) == 0 || all[len(all)-1] != 2.0 {
			t.Fatalf("the watch saw %v, want 2 last", all)
		}
		must(t, chordHost.Close(background))
		must(t, store.Close(background))
	})
}

func TestStoreStorageClosedStorageIsCollected(t *testing.T) {
	t.Run("a closed StoreStorage is collected while its store stays open", func(t *testing.T) {
		store := memoryStore(t)
		opened := func() weak.Pointer[StoreStorage] {
			storage := value(Open(background, store, "harness"))
			value(commitWrites(storage, rootWrite()))
			must(t, storage.Close(background))
			return weak.Make(storage)
		}
		ref := opened()
		for round := 0; round < 10 && ref.Value() != nil; round++ {
			time.Sleep(time.Millisecond)
			runtime.GC()
		}
		if ref.Value() != nil {
			t.Fatal("the closed StoreStorage is still reachable while its store is open")
		}
		must(t, store.Close(background))
	})
}
