package host

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// A keyed state member is a contracts.KeyedWatchable: what a factory puts in Implementation.Keyed (ExposeKeyed of a keyed
// document handle). In Pi the keyed handle is a KeyedWatchable by shape, and the cases of
// packages/chord/test/documents/keyed-slot.test.ts call `get`, `watch` and `at` on it; each test below runs one of
// those cases with those calls made through a contracts.KeyedWatchable value (its changes and retirements through the
// handle, as Pi does), so they exercise the interface's methods of the host that implements it.

// keyedWatchableOf is a keyed document handle as the KeyedWatchable a factory exposes.
func keyedWatchableOf(handle *documents.Keyed) contracts.KeyedWatchable[delta.JsonValue] {
	return ExposeKeyed(handle)
}

func watchFrames(calls []chord.WatchCall[delta.JsonValue]) []any {
	out := make([]any, len(calls))
	for i, call := range calls {
		ops := []any{}
		for _, op := range call.Ops {
			ops = append(ops, []any(op))
		}
		out[i] = []any{string(call.Kind), call.Value, ops, call.Retired}
	}
	return out
}

func watchFrame(value any, ops []any, retired bool) any {
	return []any{string(chord.WatchCallValue), value, ops, retired}
}

// TestKeyedWatchableGetIsTheDocumentAtKeyNotFoundWhenAbsent runs the get of `it("get and keys take a context; keys come in
// code point order")` of packages/chord/test/documents/keyed-slot.test.ts:100 (`memos.get("a", context)` at
// keyed-slot.test.ts:113 and `memos.get("z", context)` at keyed-slot.test.ts:114) through KeyedWatchable.Get; the keys
// come from the handle.
func TestKeyedWatchableGetIsTheDocumentAtKeyNotFoundWhenAbsent(t *testing.T) {
	memo := keyedDefinition(t, "notes.memo", 1, false, keyedTitleSchema, nil)
	var made handles
	run := restartable(t, func(_ int, self Self) { made.add(self.State(memo).(*documents.Keyed)) }, loadedOptions{})
	memos := made.at(0)
	for _, key := range []string{"b", "\U0001F600", "a", "\uFFFF"} {
		_, err := memos.Change(background, key, setTitle(key))
		must(t, err)
	}
	keyed := keyedWatchableOf(memos)
	value, found, err := keyed.Get(background, "a")
	must(t, err)
	if !found {
		t.Fatal("get(a) found nothing")
	}
	nexttest.RequireJSONEqual(t, value, jsonObject("title", "a"))
	value, found, err = keyed.Get(background, "z")
	must(t, err)
	if found || value != nil {
		t.Fatalf("get(z) = %v, %v, want absent", value, found)
	}
	var keys []string
	iterator := memos.Keys(background)
	for {
		key, ok, err := iterator.Next(background)
		must(t, err)
		if !ok {
			break
		}
		keys = append(keys, key)
	}
	nexttest.RequireJSONEqual(t, keys, []string{"a", "b", "\uFFFF", "\U0001F600"})
	closeHosts(t, run.Host)
}

// TestKeyedWatchableWatchIsNilWhenAbsentItFollowsTheAddressThroughARetirementAndARecreation runs `it("watch(key) is
// undefined when absent; it follows the address through a retirement and a recreation")` of
// packages/chord/test/documents/keyed-slot.test.ts:235 (`memos.watch("a", context)` at keyed-slot.test.ts:240 and
// :248) through KeyedWatchable.Watch.
func TestKeyedWatchableWatchIsNilWhenAbsentItFollowsTheAddressThroughARetirementAndARecreation(t *testing.T) {
	memo := keyedDefinition(t, "notes.memo", 1, false, keyedTitleSchema, nil)
	var made handles
	run := restartable(t, func(_ int, self Self) { made.add(self.State(memo).(*documents.Keyed)) }, loadedOptions{})
	memos := made.at(0)
	keyed := keyedWatchableOf(memos)
	absent, err := keyed.Watch(background, "a")
	if err != nil || absent != nil {
		t.Fatalf("watch of an absent key = %v, %v, want none", absent, err)
	}
	_, err = memos.Change(background, "a", setTitle("x"))
	must(t, err)
	watch, err := keyed.Watch(background, "a")
	if err != nil || watch == nil {
		t.Fatalf("watch = %v, %v", watch, err)
	}
	nexttest.RequireJSONEqual(t, watch.Value(), jsonObject("title", "x"))
	changes := nexttest.ChangesOf(watch)
	_, err = memos.Change(background, "a", setTitle("y"))
	must(t, err)
	deliveredOn(run.Host)
	must(t, memos.Retire(background, "a"))
	deliveredOn(run.Host)
	_, err = memos.Change(background, "a", setTitle("z"))
	must(t, err)
	deliveredOn(run.Host)
	_, err = run.Attempts.at(0).Tx(func(tx documents.Tx) (any, error) {
		if err := tx.Retire(memos, "a"); err != nil {
			return nil, err
		}
		draft, err := tx.Draft(memos, "a")
		if err != nil {
			return nil, err
		}
		return nil, draft.Set("title", "w")
	}, background)
	must(t, err)
	deliveredOn(run.Host)
	nexttest.RequireJSONEqual(t, watchFrames(changes.Items()), []any{
		watchFrame(jsonObject("title", "y"), []any{[]any{"s", []any{"title"}, "y"}}, false),
		watchFrame(jsonObject("title", ""), []any{[]any{"r", jsonObject("title", "")}}, true),
		watchFrame(jsonObject("title", "z"), []any{[]any{"r", jsonObject("title", "z")}}, false),
		watchFrame(jsonObject("title", "w"), []any{[]any{"r", jsonObject("title", "w")}}, true),
	})
	closeHosts(t, run.Host)
	select {
	case <-watch.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("the watch never ended with its host")
	}
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("closed = %v, want ended", end)
	}
}

// TestKeyedWatchableAtIsInitialWhileAbsentAStateMemberDeriveAndOnSourceRetirementArrivesWithTheFlag runs `it("is initial()
// while absent, a state member, derive and on source; retirement arrives with the flag")` of
// packages/chord/test/documents/keyed-slot.test.ts:309 (`memos.at("a")` at keyed-slot.test.ts:316, in the factory)
// through KeyedWatchable.At.
func TestKeyedWatchableAtIsInitialWhileAbsentAStateMemberDeriveAndOnSourceRetirementArrivesWithTheFlag(t *testing.T) {
	memo := keyedDefinition(t, "notes.memo", 1, false, keyedTitleSchema, nil)
	type seenCall struct {
		title   string
		retired bool
	}
	var mu sync.Mutex
	var made handles
	var pinned, upper chord.Watchable[delta.JsonValue]
	var seen []seenCall
	run := restartable(t, func(_ int, self Self) {
		memos := self.State(memo).(*documents.Keyed)
		made.add(memos)
		keyed := keyedWatchableOf(memos)
		source := keyed.At("a")
		derived := self.Derive([]any{source}, func(values []any) (delta.JsonValue, error) {
			return strings.ToUpper(values[0].(*delta.JsonObject).Value("title").(string)), nil
		})
		self.On(source, func(call chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, seenCall{call.Value.(*delta.JsonObject).Value("title").(string), call.Retired})
			return nil
		})
		mu.Lock()
		pinned, upper = source, derived
		mu.Unlock()
	}, loadedOptions{})
	memos := made.at(0)
	nexttest.RequireJSONEqual(t, pinned.Value(), jsonObject("title", ""))
	upperWatch, err := upper.Watch(background)
	must(t, err)
	changes := nexttest.ChangesOf(upperWatch)
	_, err = memos.Change(background, "a", setTitle("x"))
	must(t, err)
	deliveredOn(run.Host)
	nexttest.RequireJSONEqual(t, pinned.Value(), jsonObject("title", "x"))
	must(t, memos.Retire(background, "a"))
	deliveredOn(run.Host)
	nexttest.Flushed()
	nexttest.RequireJSONEqual(t, watchFrames(changes.Items()), []any{
		watchFrame("X", []any{[]any{"r", "X"}}, false),
		watchFrame("", []any{[]any{"r", ""}}, true),
	})
	eventually(t, "the routine's three calls", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) >= 3
	})
	mu.Lock()
	got := append([]seenCall{}, seen...)
	mu.Unlock()
	want := []seenCall{{"", false}, {"x", false}, {"", true}}
	if len(got) != len(want) {
		t.Fatalf("seen = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seen = %v, want %v", got, want)
		}
	}
	closeHosts(t, run.Host)
}
