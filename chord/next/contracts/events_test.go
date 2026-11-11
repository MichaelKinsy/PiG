package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports the event cases of packages/chord/test/contracts/{accepts,declare,evolve,fingerprint}.test.ts (Pi 8d30e6306,
// decision "Events: fire-and-forget members").
//
// Designed out as everywhere in this package: expectTypeOf/@ts-expect-error lines (type-level only) and inputs a typed
// Go declaration cannot express (a method under `events`, a mapping that is no function).

func eventsOf(properties ...contracts.Property) contracts.VariantDeclaration {
	return contracts.VariantDeclaration{Events: properties}
}

func eventEntry(member string, mapping contracts.EventMapping) contracts.Entry[contracts.EventMapping] {
	return contracts.Entry[contracts.EventMapping]{Name: member, Value: mapping}
}

func variantEvents(variant string, members ...contracts.Entry[contracts.EventMapping]) contracts.Entry[[]contracts.Entry[contracts.EventMapping]] {
	return contracts.Entry[[]contracts.Entry[contracts.EventMapping]]{Name: variant, Value: members}
}

func eventTable(entries ...contracts.Entry[[]contracts.Entry[contracts.EventMapping]]) contracts.AcceptsTable {
	return table(version("1", contracts.VersionMappings{VariantEvents: entries}))
}

func TestAcceptsEventMappings(t *testing.T) {
	sample := func() contracts.Schema { return contracts.Obj(contracts.Prop("y", contracts.Int())) }
	timed := func() contracts.Schema {
		return contracts.Obj(contracts.Prop("y", contracts.Int()), contracts.Prop("t", contracts.Int()))
	}
	matchV1 := func(t *testing.T) *contracts.Contract {
		return remote(t, "match", 1, noParams(),
			contracts.Var("player", eventsOf(contracts.Prop("paddle", sample()), contracts.Prop("ball", sample()))),
			contracts.Var("watcher", eventsOf(contracts.Prop("paddle", sample()))))
	}
	mapped := contracts.EventMapping{Func: fn}
	refuse := contracts.EventMapping{Refuse: true}

	t.Run("need a function or refuse for a changed event member, by variant", func(t *testing.T) {
		v2 := evolveWith(t, matchV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Event("player", "paddle", timed()) })
		for _, mapping := range []contracts.EventMapping{mapped, refuse} {
			checked := must(contracts.CheckAccepts(v2, eventTable(variantEvents("player", eventEntry("paddle", mapping)))))
			if len(checked.Versions) != 1 {
				t.Fatalf("versions = %+v", checked.Versions)
			}
		}
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{})))
		wantError(t, err, "match accepts[1].events.player: missing mapping")
		_, err = contracts.CheckAccepts(v2, eventTable(variantEvents("player", eventEntry("paddle", contracts.EventMapping{}))))
		wantError(t, err, `match accepts[1].events.player.paddle: expected a function or "refuse"`)
		// One variant's change needs nothing for another (its refusal breaks only that variant's binding).
		_, err = contracts.CheckAccepts(v2, eventTable(variantEvents("player", eventEntry("paddle", refuse)), variantEvents("watcher")))
		wantError(t, err, "match accepts[1].events.watcher: unnecessary mapping")
	})

	t.Run("need nothing for a removed or an added event member; a re-added one counts as changed", func(t *testing.T) {
		removed := evolveWith(t, matchV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.RemoveEvent("player", "ball").AddEvent("watcher", "ball", sample())
		})
		if checked := must(contracts.CheckAccepts(removed, nil)); len(checked.Versions) != 0 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		if checked := must(contracts.CheckAccepts(removed, table(version("1", contracts.VersionMappings{})))); len(checked.Versions) != 1 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		_, err := contracts.CheckAccepts(removed, eventTable(variantEvents("player", eventEntry("ball", mapped))))
		wantError(t, err, "match accepts[1].events.player: unnecessary mapping")
		readded := evolveWith(t, removed, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.AddEvent("player", "ball", timed()) })
		_, err = contracts.CheckAccepts(readded, table(version("1", contracts.VersionMappings{})))
		wantError(t, err, "match accepts[1].events.player: missing mapping")
		if checked := must(contracts.CheckAccepts(readded, eventTable(variantEvents("player", eventEntry("ball", mapped))))); len(checked.Versions) != 1 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
	})

	t.Run("need an identity mapping for a no-op edit (by edit, never by schema)", func(t *testing.T) {
		v2 := evolveWith(t, matchV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Event("watcher", "paddle", sample()) })
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{})))
		wantError(t, err, "match accepts[1].events.watcher: missing mapping")
		if checked := must(contracts.CheckAccepts(v2, eventTable(variantEvents("watcher", eventEntry("paddle", mapped))))); len(checked.Versions) != 1 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
	})

	t.Run("are flat in a local contract", func(t *testing.T) {
		v1 := local(t, "store", 1, noParams(), contracts.Mem("paddle", contracts.Event(sample())))
		v2 := must(evolveLocal(v1, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.Event("paddle", timed()) }))
		flat := table(version("1", contracts.VersionMappings{Events: []contracts.Entry[contracts.EventMapping]{eventEntry("paddle", mapped)}}))
		if checked := must(contracts.CheckAccepts(v2, flat)); len(checked.Versions) != 1 {
			t.Fatalf("versions = %+v", checked.Versions)
		}
		_, err := contracts.CheckAccepts(v2, table(version("1", contracts.VersionMappings{Events: []contracts.Entry[contracts.EventMapping]{eventEntry("members", mapped)}})))
		wantError(t, err, "store accepts[1].events.members: unnecessary mapping")
		// The two shapes of upstream's `events` section: a remote table maps per variant, a local one per member.
		_, err = contracts.CheckAccepts(v2, eventTable(variantEvents("members", eventEntry("paddle", mapped))))
		wantError(t, err, "store accepts[1].variantEvents: a local table has no variants (Events)")
		remoteV2 := evolveWith(t, matchV1(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Event("watcher", "paddle", sample()) })
		_, err = contracts.CheckAccepts(remoteV2, flat)
		wantError(t, err, "match accepts[1].events: a remote table maps events per variant (VariantEvents)")
	})
}

func TestDeclareEventMembers(t *testing.T) {
	paddle := func() contracts.Schema {
		return contracts.Obj(contracts.Prop("peer", contracts.Str()), contracts.Prop("y", contracts.Int()))
	}

	t.Run("declare events in a remote variant and with the local marker: their own kind", func(t *testing.T) {
		match := remote(t, "app.match", 1, noParams(),
			contracts.Var("player", contracts.VariantDeclaration{State: state(contracts.Prop("score", contracts.Int())), Events: []contracts.Property{contracts.Prop("paddle", paddle())}}),
			contracts.Var("watcher", contracts.VariantDeclaration{Events: []contracts.Property{}}))
		if got := keys(currentVariants(t, match).Value("player").Events); len(got) != 1 || got[0] != "paddle" {
			t.Fatalf("events = %v", got)
		}
		store := local(t, "app.store", 1, noParams(), contracts.Mem("score", contracts.Int()), contracts.Mem("paddle", contracts.Event(paddle())))
		members := currentVariants(t, store).Value(contracts.LocalVariant)
		if got, want := keys(members.Events), []string{"paddle"}; len(got) != 1 || got[0] != want[0] {
			t.Fatalf("events = %v", got)
		}
		if got := keys(members.State); len(got) != 1 || got[0] != "score" {
			t.Fatalf("state = %v", got)
		}
		names := contracts.VariantNamesOf(members)
		if len(names.Events) != 1 || names.Events[0] != "paddle" || len(names.State) != 1 {
			t.Fatalf("names = %+v", names)
		}
	})

	t.Run("keep one kind per name across variants and the history", func(t *testing.T) {
		_, err := declareRemote("app.match", 1, noParams(),
			contracts.Var("a", contracts.VariantDeclaration{State: state(contracts.Prop("paddle", paddle()))}),
			contracts.Var("b", eventsOf(contracts.Prop("paddle", paddle()))))
		wantError(t, err, `app.match.b.events.paddle: "paddle" is a state member; its kind never changes`)
		store := local(t, "app.store", 1, noParams(), contracts.Mem("paddle", contracts.Event(paddle())))
		without := must(evolveLocal(store, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.RemoveEvent("paddle") }))
		_, err = evolveLocal(without, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.AddState("paddle", paddle()) })
		wantError(t, err, `"paddle" is an event member; its kind never changes`)
	})

	t.Run("allow an event's schema to differ per variant (redaction), and refuse bytes", func(t *testing.T) {
		_, err := declareRemote("app.match", 1, noParams(),
			contracts.Var("a", eventsOf(contracts.Prop("paddle", paddle()))),
			contracts.Var("b", eventsOf(contracts.Prop("paddle", contracts.Obj(contracts.Prop("y", contracts.Int()))))))
		wantNoError(t, err)
		_, err = contracts.Local("app.store", contracts.LocalDeclaration{Version: 1, Params: noParams(), Members: []contracts.LocalEntry{
			contracts.Mem("frame", contracts.Event(contracts.Obj(contracts.Prop("data", contracts.Bytes())))),
		}})
		wantError(t, err, "app.store.members.frame: an event's values are JSON only (no bytes())")
		_, err = declareRemote("app.match", 1, noParams(),
			contracts.Var("a", eventsOf(contracts.Prop("x", contracts.Bytes()))))
		wantError(t, err, "app.match.a.events.x: an event's values are JSON only (no bytes())")
	})
}

func TestEvolveEventEdits(t *testing.T) {
	sample := func() contracts.Schema { return contracts.Obj(contracts.Prop("y", contracts.Int())) }
	timed := func() contracts.Schema {
		return contracts.Obj(contracts.Prop("y", contracts.Int()), contracts.Prop("t", contracts.Int()))
	}
	match := func(t *testing.T) *contracts.Contract {
		return remote(t, "match", 1, noParams(),
			contracts.Var("player", eventsOf(contracts.Prop("paddle", sample()))),
			variant("watcher", state(contracts.Prop("score", contracts.Int()))))
	}
	eventNames := func(contract *contracts.Contract, variant string) [][]string {
		var out [][]string
		for _, version := range dataOf(t, contract).Versions {
			out = append(out, keys(version.Variants.Value(variant).Events))
		}
		return out
	}
	same := func(got, want [][]string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if len(got[i]) != len(want[i]) {
				return false
			}
			for j := range got[i] {
				if got[i][j] != want[i][j] {
					return false
				}
			}
		}
		return true
	}

	t.Run("change, add and remove an event member of a variant, each a change of its own kind", func(t *testing.T) {
		v2 := evolveWith(t, match(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.Event("player", "paddle", timed()).AddEvent("watcher", "ball", sample())
		})
		v3 := evolveWith(t, v2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveEvent("player", "paddle") })
		if got := eventNames(v3, "player"); !same(got, [][]string{{"paddle"}, {"paddle"}, {}}) {
			t.Fatalf("player events by version = %v", got)
		}
		changes := dataOf(t, v3).Versions[1].Changes
		if len(changes) != 2 || changes[0] != (contracts.Change{Variant: "player", Member: "paddle", Side: contracts.SideEvent}) ||
			changes[1] != (contracts.Change{Variant: "watcher", Member: "ball", Side: contracts.SideEvent}) {
			t.Fatalf("changes = %+v", changes)
		}
		seen := map[string]bool{}
		for _, version := range dataOf(t, v3).Versions {
			seen[version.Fingerprint] = true
		}
		if len(seen) != 3 {
			t.Fatalf("fingerprints = %v", seen)
		}
	})

	t.Run("take no variant in a local contract", func(t *testing.T) {
		store := local(t, "store", 1, noParams(), contracts.Mem("paddle", contracts.Event(sample())))
		v2 := must(evolveLocal(store, func(e *contracts.LocalEditor) *contracts.LocalEditor {
			return e.Event("paddle", timed()).AddEvent("ball", sample())
		}))
		v3 := must(evolveLocal(v2, func(e *contracts.LocalEditor) *contracts.LocalEditor { return e.RemoveEvent("ball") }))
		if got := eventNames(v3, contracts.LocalVariant); !same(got, [][]string{{"paddle"}, {"paddle", "ball"}, {"paddle"}}) {
			t.Fatalf("events by version = %v", got)
		}
	})

	t.Run("refuse an unknown event member, one declared already, and bytes", func(t *testing.T) {
		_, err := contracts.Evolve(match(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.Event("player", "missing", sample()) })
		wantError(t, err, "no such event member")
		_, err = contracts.Evolve(match(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddEvent("watcher", "score", sample())
		})
		wantError(t, err, "already declared")
		_, err = contracts.Evolve(match(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor { return e.RemoveEvent("player", "missing") })
		wantError(t, err, "no such event member")
		_, err = contracts.Evolve(match(t), func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
			return e.AddEvent("player", "frame", contracts.Obj(contracts.Prop("data", contracts.Bytes())))
		})
		wantError(t, err, "an event's values are JSON only")
	})
}

func TestFingerprintsOfEventMembers(t *testing.T) {
	declare := func(declaration contracts.VariantDeclaration) *contracts.Contract {
		return remote(t, "n", 1, noParams(), contracts.Var("a", declaration))
	}
	s := []contracts.Property{contracts.Prop("s", contracts.Int())}
	plain := declare(contracts.VariantDeclaration{State: s})
	empty := declare(contracts.VariantDeclaration{State: s, Events: []contracts.Property{}})
	withEvent := declare(contracts.VariantDeclaration{State: s, Events: []contracts.Property{contracts.Prop("e", contracts.Int())}})
	asState := declare(contracts.VariantDeclaration{State: append(append([]contracts.Property{}, s...), contracts.Prop("e", contracts.Int()))})
	if empty.Fingerprint != plain.Fingerprint {
		t.Fatalf("an empty events section changed the fingerprint: %s vs %s", empty.Fingerprint, plain.Fingerprint)
	}
	if len(map[string]bool{plain.Fingerprint: true, withEvent.Fingerprint: true, asState.Fingerprint: true}) != 3 {
		t.Fatalf("fingerprints: %s %s %s", plain.Fingerprint, withEvent.Fingerprint, asState.Fingerprint)
	}
	store := local(t, "n", 1, noParams(), contracts.Mem("e", contracts.Event(contracts.Int())))
	if store.Fingerprint == local(t, "n", 1, noParams(), contracts.Mem("e", contracts.Int())).Fingerprint {
		t.Fatal("an event member has the fingerprint of a state member")
	}
}
