package host

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/next/store/claims.test.ts:45 ("an object claims its tables and documents when it
// declares them, and releases them at its end"): the two cases of that file on a bare Claims are in
// chord/next/store/claims_test.go.
//
// pi: packages/chord/src/next/store/claims.ts

func TestClaimsAnObjectClaimsItsTablesAndDocumentsWhenItDeclaresThemAndReleasesThemAtItsEnd(t *testing.T) {
	chordStore := memoryStore(t)
	note, err := documents.DefineState("note", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1, Persist: true,
		Initial:        func() *delta.JsonObject { return jsonObject("n", 0.0) },
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return true },
	})
	must(t, err)
	edits, err := store.DefineTable("edits", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("id", store.Str())}, Key: []string{"id"}})
	must(t, err)
	notes := mustContract(contracts.Local("notes.store", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(notes, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.(StoreSelf).State(note)
			self.(StoreSelf).Table(edits)
			return storeImplementation(), nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	mustGet(t, host, notes, jsonObject())

	other := &store.NamedClaimant{Label: "other"}
	if err := chordStore.Claims.Table("notes.store:edits", other); err == nil || !strings.Contains(err.Error(), "claimed by notes.store") {
		t.Fatalf("table claim = %v, want it claimed by notes.store", err)
	}
	if err := chordStore.Claims.Documents("notes.store", "chord", other); err == nil || !strings.Contains(err.Error(), "claimed") {
		t.Fatalf("documents claim = %v, want it claimed", err)
	}
	closeHosts(t, host)
	if chordStore.Claims.Held() {
		t.Fatal("claims are still held after the host closed")
	}
	if err := chordStore.Claims.Table("notes.store:edits", other); err != nil {
		t.Fatalf("table claim after close: %v", err)
	}
	must(t, chordStore.Close(background))
}
