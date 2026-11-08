package drafts_test

import (
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	contract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/test/p2/drafts.test.ts, "drafts store": drafts per conversation and author, each handed out once.

func author(kind, id, name string) *delta.JsonObject {
	return delta.JsonObjectOf("kind", kind, "id", id, "name", name)
}

func texts(value delta.JsonValue, key string) []string {
	object, _ := value.(*delta.JsonObject)
	items, _ := object.Value(key).([]any)
	if items == nil {
		return nil
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item.(*delta.JsonObject).Value("text").(string)
	}
	return out
}

func ids(value delta.JsonValue, key string) []any {
	object, _ := value.(*delta.JsonObject)
	items, _ := object.Value(key).([]any)
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item.(*delta.JsonObject).Value("id")
	}
	return out
}

func claimed(t *testing.T, store *host.Reference, ctx host.Ctx, conversation float64, authorID string, claimIDs []any) []any {
	t.Helper()
	result, err := store.Call("claim", delta.JsonObjectOf("conversation", conversation, "authorId", authorID, "ids", claimIDs), ctx)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := result.(*delta.JsonObject).Value("texts").([]any)
	if list == nil {
		return []any{}
	}
	return list
}

func TestKeepsDraftsPerConversationAndAuthorAndHandsEachOutOnce(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "sample"))
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Address: &address, Worker: true, Facets: []host.Facet{drafts.Facet()}})
	_, err := fixture.Root(func(ctx host.Ctx) (any, error) {
		store, err := ctx.Get(contract.DraftStore, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		alice, owner := author("guest", "alice", "Alice"), author("owner", "owner", "Owner")
		item := func(who *delta.JsonObject, text string) any { return delta.JsonObjectOf("author", who, "text", text) }
		if _, err := store.Call("add", delta.JsonObjectOf("conversation", float64(1), "items", []any{item(alice, "a1"), item(owner, "o1"), item(alice, "a2")}), ctx); err != nil {
			return nil, err
		}
		if _, err := store.Call("add", delta.JsonObjectOf("conversation", float64(2), "items", []any{item(alice, "child")}), ctx); err != nil {
			return nil, err
		}
		drafts, err := p2test.Settled(store.Member("drafts"), 0, 0)
		if err != nil {
			return nil, err
		}
		if got := texts(drafts, "1:alice"); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
			t.Errorf("1:alice = %v", got)
		}
		if got := texts(drafts, "1:owner"); !reflect.DeepEqual(got, []string{"o1"}) {
			t.Errorf("1:owner = %v", got)
		}
		aliceIDs := ids(drafts, "1:alice")
		// Another author's claim takes nothing.
		if got := claimed(t, store, ctx, 1, "owner", aliceIDs); len(got) != 0 {
			t.Errorf("another author claimed %v", got)
		}
		if got := claimed(t, store, ctx, 1, "alice", aliceIDs); !reflect.DeepEqual(got, []any{"a1", "a2"}) {
			t.Errorf("claim = %v", got)
		}
		// A second tab claiming the same drafts gets nothing.
		if got := claimed(t, store, ctx, 1, "alice", aliceIDs); len(got) != 0 {
			t.Errorf("second claim = %v", got)
		}
		// Two tabs claiming at once: each draft goes to one of them.
		if _, err := store.Call("add", delta.JsonObjectOf("conversation", float64(3), "items", []any{item(alice, "once")}), ctx); err != nil {
			return nil, err
		}
		drafts, err = p2test.Settled(store.Member("drafts"), 0, 0)
		if err != nil {
			return nil, err
		}
		raced := ids(drafts, "3:alice")
		var wait sync.WaitGroup
		var mu sync.Mutex
		var all []any
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				got := claimed(t, store, ctx, 3, "alice", raced)
				mu.Lock()
				all = append(all, got...)
				mu.Unlock()
			}()
		}
		wait.Wait()
		if !reflect.DeepEqual(all, []any{"once"}) {
			t.Errorf("racing claims = %v, want exactly one \"once\"", all)
		}
		final, _ := store.Member("drafts").Value()
		if got := final.(*delta.JsonObject).Value("1:alice"); got != nil {
			t.Errorf("1:alice not removed: %v", got)
		}
		if got := texts(final, "2:alice"); !reflect.DeepEqual(got, []string{"child"}) {
			t.Errorf("2:alice = %v", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Drafts is the caller's own drafts: participants only.
func TestDraftsRefusesAViewGuest(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "sample"))
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Address: &address, Worker: true, Facets: []host.Facet{drafts.Facet()}})
	guest := fixture.Client(p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: "sample", Sessions: map[string]p2runtime.GuestMode{"sample": p2runtime.GuestView}})
	ref, err := p2test.HoldIn(guest, contract.Drafts, delta.JsonObjectOf("session", "sample", "conversation", float64(1)), "author")
	if err == nil {
		failure, _ := p2test.FailureOf(ref)
		if failure == nil {
			t.Fatal("a view guest must not hold the author variant")
		}
	}
}
