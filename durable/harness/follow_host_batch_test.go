// A followed Harness on a Chord host: the calls one Durable commit makes to two followed sources reach a derive over both as one
// batch (Pi follow.ts: "calls outside one are grouped into one batch on the next microtask, so a derive over two views of one
// commit computes once with both changes"). Pi's Durable publishes a commit to every observer in one synchronous step of the
// host's one thread; Go's Durable publishes from its Session writer goroutine, so an adapter gives the Harness a Storage whose
// publication scope (durable.PublishingStorage, Pi's `Storage.publishing`) is a section of the host, Host.Section.

package harness

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// rrSectionStorage is a Storage whose publication scope is a section of host.
type rrSectionStorage struct {
	durable.Storage
	host chordhost.Host
}

func (scoped rrSectionStorage) Publishing(_ context.Context, publish func()) {
	scoped.host.Section(publish)
}

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

func TestOneCommitChangingTwoFollowedViewsReachesADeriveAsOneBatch(t *testing.T) {
	done := rrDoneTask()
	host := rrCreateHost(t)
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
					opened, err := OpenHarness(ctx, rrSectionStorage{Storage: storage.NewMemoryStorage(), host: host}, HarnessOptions{Models: ai.CreateModels(), Registry: registry})
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
	if _, err := reference.Call("both", nil, testContext); err != nil {
		t.Fatal(err)
	}
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
