package app

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/node/app/users.test.ts (Pi main c23b9ec1): tests of the users plugin (slice 13 §3): the app makes it
// with its profile schema; the server reaches `Users`; profiles persist. The facet is a core facet of the server here (D456).

func TestUsersKeepAccountsByIDWithTheAppsProfileListedUpdatedRemovedAndKeptAcrossStarts(t *testing.T) {
	users, facet, err := DefineUsers(contracts.Obj(contracts.Prop("name", contracts.Str()), contracts.Prop("title", contracts.Opt(contracts.Str()))))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	first := newKitHost(t, dir, host.HostOptions{}, host.HostFacet{Name: "users", Facet: facet})
	ref, err := first.host.Get(background, users, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	update := func(id string, profile delta.JsonValue) error {
		_, err := ref.Call("update", delta.JsonObjectOf("id", id, "profile", profile), background)
		return err
	}
	for _, step := range []struct {
		id      string
		profile *delta.JsonObject
	}{
		{"u1", delta.JsonObjectOf("name", "Ada")},
		{"u2", delta.JsonObjectOf("name", "Grace", "title", "Admiral")},
		{"u1", delta.JsonObjectOf("name", "Ada L.")},
	} {
		if err := update(step.id, step.profile); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ref.Call("get", "u1", background)
	if err != nil || !reflect.DeepEqual(got, delta.JsonObjectOf("name", "Ada L.")) {
		t.Fatalf("get u1 = %v, %v", got, err)
	}
	if got, err = ref.Call("get", "nobody", background); err != nil || got != nil {
		t.Fatalf("get nobody = %v, %v", got, err)
	}
	if _, err := ref.Call("remove", "u2", background); err != nil {
		t.Fatal(err)
	}
	list, err := ref.Member("list").Value()
	wantList := []any{delta.JsonObjectOf("id", "u1", "profile", delta.JsonObjectOf("name", "Ada L."))}
	if err != nil || !reflect.DeepEqual(list, wantList) {
		t.Fatalf("list = %v, %v", list, err)
	}
	if _, invalid := data.IsChordErrorCode(update("u3", delta.JsonObjectOf("title", "no name")), data.CodeInvalid); !invalid {
		t.Fatal("a profile without its name was accepted")
	}
	if _, invalid := data.IsChordErrorCode(update("__proto__", delta.JsonObjectOf("name", "x")), data.CodeInvalid); !invalid {
		t.Fatal("__proto__ was accepted as a user id")
	}
	ref.Release()
	first.close()

	second := newKitHost(t, dir, host.HostOptions{}, host.HostFacet{Name: "users", Facet: facet})
	again, err := second.host.Get(background, users, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	list, err = again.Member("list").Value()
	if err != nil || !reflect.DeepEqual(list, wantList) {
		t.Fatalf("list after a restart = %v, %v", list, err)
	}
	again.Release()
	second.close()
}
