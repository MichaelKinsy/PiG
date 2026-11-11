package host

// pi: packages/chord/test/next/examples/21-persisted-documents.ts

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 21-persisted-documents.ts -------------------------------------------------------------------------------------

func TestExample21PersistedDocuments(t *testing.T) {
	// A persisted singleton: loaded once when its object activates; every 20th stored change is a new base.
	tags := exDefineState("tags", documents.StateOptions{
		Schema:  exObj(exProp("tags", exArr(exStr()))),
		Initial: func() *delta.JsonObject { return jsonObject("tags", []any{}) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 20
		},
	})
	// A persisted keyed document: each key loaded on its first read, then cached until idle for ten minutes.
	notes := exDefineState("notes", documents.StateOptions{
		Schema:         exObj(exProp("body", exStr())),
		Initial:        func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:          true,
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	notebook := exLocal("example.notebook", contracts.Params{},
		contracts.Mem("tags", exObj(exProp("tags", exArr(exStr())))),
		contracts.Mem("write", contracts.NewMethod(exObj(exProp("id", exStr()), exProp("body", exStr()), exProp("tag", exStr())))),
		contracts.Mem("read", contracts.NewMethod(exStr(), exStr())))
	facet := func(h FacetHost, _ FacetSelf) error {
		// Persisted documents need a stable identity, as tables do: a local type with empty params.
		return h.Serve(notebook, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			tagsDoc := stateOf(self, tags)
			notesDoc := self.State(notes).(*documents.Keyed)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"tags": tagsDoc.Member()}, map[string]contracts.MethodFunction{
				"write": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					// Both documents commit in one store step, or neither does.
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						draft, err := tx.Draft(notesDoc, exParam(args, "id"))
						if err != nil {
							return nil, err
						}
						if err := draft.Set("body", args.Value("body")); err != nil {
							return nil, err
						}
						tagsDraft, err := tx.Draft(tagsDoc.s)
						if err != nil {
							return nil, err
						}
						for _, existing := range tagsDraft.Array("tags").Snapshot() {
							if existing == args.Value("tag") {
								return nil, nil
							}
						}
						_, err = tagsDraft.Array("tags").Push(args.Value("tag"))
						return nil, err
					}, ctx)
					return nil, err
				}),
				"read": func(id delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					body, err := notesDoc.Get(ctx, id.(string))
					if err != nil || body == nil {
						return "(none)", err
					}
					return body.Value("body"), nil
				},
			}), nil
		}, ServeOptions{})
	}
	// session is a host on the store at path, run until work is done with the notebook.
	session := func(path string, work func(notebook *Reference)) {
		st := exFileStore(t, path)
		host, _ := exOpenHost(t, loadedOptions{Store: st}, facet)
		work(exGet(t, host, notebook, jsonObject()))
		exCloseHost(t, host)
		exCloseStore(t, st)
	}
	path := t.TempDir() + "/notebook.db"
	session(path, func(ref *Reference) {
		exCall(t, ref, "write", jsonObject("id", "groceries", "body", "oat milk", "tag", "home"))
		exCall(t, ref, "write", jsonObject("id", "standup", "body", "demo the store", "tag", "work"))
		exEqual(t, "first host, tags", exValue(t, ref, "tags").(*delta.JsonObject).Value("tags"), exJSON(t, `["home","work"]`))
	})
	// A new host on the same file: the documents come back from the store.
	session(path, func(ref *Reference) {
		exEqual(t, "second host, tags", exValue(t, ref, "tags").(*delta.JsonObject).Value("tags"), exJSON(t, `["home","work"]`))
		exEqual(t, "second host, standup", exCall(t, ref, "read", "standup"), "demo the store")
	})
}
