package documents

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/documents/handles.test.ts
//
// Pi's `restartable(objects, factory)` host is the harness's object + attempt; `fail()` is attempt.Fail().

func handlesDefinitions(t *testing.T) (body, notes *StateDefinition) {
	t.Helper()
	textSchema := contracts.Obj(contracts.Prop("text", contracts.Str()))
	initial := func() *JsonObject { return obj("text", "") }
	body, err := DefineState("body", StateOptions{Schema: textSchema, Version: 1, Initial: initial})
	if err != nil {
		t.Fatal(err)
	}
	notes, err = DefineState("notes", StateOptions{Schema: textSchema, Version: 1, Initial: initial, Keyed: true})
	if err != nil {
		t.Fatal(err)
	}
	return body, notes
}

func handlesSetText(text string) func(*delta.Object) (any, error) {
	return func(draft *delta.Object) (any, error) { return nil, draft.Set("text", text) }
}

func TestKeyedHandlesListKeysInCodePointOrderNotUTF16Order(t *testing.T) {
	_, notesDefinition := handlesDefinitions(t)
	var notes *Keyed
	activated(t, objectOptions{}, func(a *attempt) { notes = a.Keyed(notesDefinition) })
	// U+FF5E sorts before U+1F600 by code point; as UTF-16 its unit 0xFF5E sorts after the surrogate 0xD83D.
	for _, key := range []string{"\U0001F600", "\uFF5E", "b", "a"} {
		if _, err := notes.Change(background, key, handlesSetText(key)); err != nil {
			t.Fatal(err)
		}
	}
	nexttest.RequireJSONEqual(t, slotKeysOf(t, notes), []any{"a", "b", "\uFF5E", "\U0001F600"})
	got, err := notes.Get(background, "\uFF5E")
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "\uFF5E"))
}

// TestSealedHandlesStillReadSynchronouslyAndFromMemoryChangesAndRetirementsAreRefusedReleased runs `it("still read
// synchronously and from memory; changes and retirements are refused released")` of
// packages/chord/test/documents/handles.test.ts:48. The handles are held as what `self.state` hands out, the
// SingletonHandle and KeyedHandle interfaces: `body.change` and `body.retire` (handles.test.ts:70-71) are
// SingletonHandle.Change and Retire, `notes.change` and `notes.retire` (handles.test.ts:72-73) KeyedHandle.Change and Retire.
func TestSealedHandlesStillReadSynchronouslyAndFromMemoryChangesAndRetirementsAreRefusedReleased(t *testing.T) {
	bodyDefinition, notesDefinition := handlesDefinitions(t)
	var body SingletonHandle
	var notes KeyedHandle
	o, first := activated(t, objectOptions{label: "app.restartable(1)"}, func(a *attempt) {
		body = a.Singleton(bodyDefinition)
		notes = a.Keyed(notesDefinition)
	})
	if _, err := body.Change(background, handlesSetText("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Change(background, "k", handlesSetText("y")); err != nil {
		t.Fatal(err)
	}
	first.Fail()
	value, err := slotValue(o, body)
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("text", "x"))
	got, err := notes.Get(background, "k")
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "y"))
	nexttest.RequireJSONEqual(t, slotKeysOf(t, notes), []any{"k"})
	released := obj("code", "released", "reason", "ended", "message", "app.restartable(1): ended")
	noop := func(*delta.Object) (any, error) { return nil, nil }
	_, err = body.Change(background, noop)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	nexttest.RequireJSONEqual(t, rejection(t, body.Retire(background)), released)
	_, err = notes.Change(background, "k", noop)
	nexttest.RequireJSONEqual(t, rejection(t, err), released)
	nexttest.RequireJSONEqual(t, rejection(t, notes.Retire(background, "k")), released)
}

func handlesSession(t *testing.T) *contracts.HostKind {
	t.Helper()
	session, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func handlesSessionAddress(session *contracts.HostKind, id string) contracts.Address {
	return session.Address(obj("id", id))
}

func handlesMeta(t *testing.T, session *contracts.HostKind) *StateDefinition {
	t.Helper()
	return mustDefine(t, "meta", StateOptions{
		Schema:  contracts.Obj(contracts.Prop("text", contracts.Str())),
		Version: 1,
		Initial: func() *JsonObject { return obj("text", "") },
		Keyed:   session,
	})
}

// handlesRefused requires err to be the `invalid/args` refusal of a key that is no address of session.
func handlesRefused(t *testing.T, err error) {
	t.Helper()
	nexttest.RequireJSONEqual(t, rejection(t, err), obj("code", "invalid", "reason", "args", "message", "meta: key: expected an address of session"))
}

// TestHostKeyedHandlesTakeAndListAddressesOfTheirKindStoredByTheirKeys runs `it("take and list addresses of their
// kind, stored by their keys")` of the `describe("host-keyed handles")` of packages/chord/test/documents/handles.test.ts.
// The `KeyedHandle<{ text: string }, Address<typeof Session>>` and `Watchable<{ text: string }>` annotations are
// designed out (the Go handle takes `any` keys).
func TestHostKeyedHandlesTakeAndListAddressesOfTheirKindStoredByTheirKeys(t *testing.T) {
	session := handlesSession(t)
	var meta *Keyed
	var pinned *KeySource
	o, _ := activated(t, objectOptions{}, func(a *attempt) {
		meta = a.Keyed(handlesMeta(t, session))
		var err error
		if pinned, err = meta.At(handlesSessionAddress(session, "S1")); err != nil {
			t.Fatal(err)
		}
	})
	for _, entry := range [][2]string{{"S1", "one"}, {"S2", "two"}} {
		if _, err := meta.Change(background, handlesSessionAddress(session, entry[0]), handlesSetText(entry[1])); err != nil {
			t.Fatal(err)
		}
	}
	got, err := meta.Get(background, handlesSessionAddress(session, "S2"))
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, got, obj("text", "two"))
	var keys []any
	iterator := meta.Keys(background)
	for {
		key, ok, err := iterator.Next(background)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		keys = append(keys, key)
	}
	if len(keys) != 2 {
		t.Fatalf("keys = %v, want two addresses", keys)
	}
	for index, id := range []string{"S1", "S2"} {
		address, isAddress := keys[index].(contracts.Address)
		if want := handlesSessionAddress(session, id); !isAddress || address.Kind != want.Kind || address.Key != want.Key {
			t.Fatalf("keys[%d] = %#v, want %#v", index, keys[index], want)
		}
		nexttest.RequireJSONEqual(t, address.Params, obj("id", id))
	}
	var value delta.JsonValue
	o.Do(func() { value, err = pinned.Value() })
	if err != nil {
		t.Fatal(err)
	}
	nexttest.RequireJSONEqual(t, value, obj("text", "one"))
	watch, err := meta.Watch(background, handlesSessionAddress(session, "S1"))
	if err != nil || watch == nil {
		t.Fatalf("watch = %v, %v", watch, err)
	}
	nexttest.RequireJSONEqual(t, watch.Value(), obj("text", "one"))
	if err := meta.Retire(background, handlesSessionAddress(session, "S1")); err != nil {
		t.Fatal(err)
	}
	if got, err = meta.Get(background, handlesSessionAddress(session, "S1")); err != nil || got != nil {
		t.Fatalf("get after retire = %v, %v, want none", got, err)
	}
	// The watch ends with the object (Pi's host is closed by the test harness).
	o.End()
	ksWaitClosed(t, watch.Closed())
}

// TestHostKeyedHandlesRefuseStringsAndAddressesOfAnotherKindThroughTheHandleAndInATransaction runs `it("refuse strings
// and addresses of another kind, through the handle and in a transaction")` of the same describe. `key as never` casts
// are the Go handle's `any` keys; `tx(undefined)` is a transaction's draft without a key.
func TestHostKeyedHandlesRefuseStringsAndAddressesOfAnotherKindThroughTheHandleAndInATransaction(t *testing.T) {
	session := handlesSession(t)
	var meta *Keyed
	_, first := activated(t, objectOptions{}, func(a *attempt) { meta = a.Keyed(handlesMeta(t, session)) })
	draft := func(keys ...any) error {
		_, err := first.Tx(func(tx Tx) (any, error) { return tx.Draft(meta, keys...) }, background)
		return err
	}
	noop := func(*delta.Object) (any, error) { return nil, nil }
	_, err := meta.Get(background, "session|S1")
	handlesRefused(t, err)
	_, err = meta.Change(background, contracts.Address{Kind: "user", Params: obj("id", "S1")}, noop)
	handlesRefused(t, err)
	handlesRefused(t, draft("session|S1"))
	if _, err := meta.At("session|S1"); err == nil || !strings.Contains(err.Error(), "expected an address of session") {
		t.Fatalf("at(string) = %v, want a refusal", err)
	}
	// The empty key and an omitted one are no addresses either (they never reach the store as `host: ""`).
	_, err = meta.Change(background, "", noop)
	handlesRefused(t, err)
	handlesRefused(t, draft(""))
	handlesRefused(t, draft())
}
