package host

// pi: packages/chord/test/next/examples/02-documents.ts

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 02-documents.ts -----------------------------------------------------------------------------------------------

func TestExample02Documents(t *testing.T) {
	item := exObj(exProp("id", exInt()), exProp("text", exStr()))
	todosDoc := exDefineState("todos", documents.StateOptions{
		Schema:  exObj(exProp("items", exArr(item)), exProp("next", exInt())),
		Initial: func() *delta.JsonObject { return jsonObject("items", []any{}, "next", float64(1)) },
	})
	notesDoc := exDefineState("notes", documents.StateOptions{
		Schema:  exObj(exProp("body", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("body", "") },
		Keyed:   true,
	})
	store := exLocal("example.todos", contracts.Params{},
		contracts.Mem("todos", exObj(exProp("items", exArr(item)), exProp("next", exInt()))),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("text", exStr()), exProp("note", exOpt(exStr()))), exInt())),
		contracts.Mem("note", contracts.NewMethod(exObj(exProp("id", exInt())), exStr())),
		contracts.Mem("clear", contracts.NewMethod()))
	host, _ := exOpenHost(t, loadedOptions{}, func(host FacetHost, _ FacetSelf) error {
		return host.Serve(store, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			todos := stateOf(self, todosDoc)
			notes := self.State(notesDoc).(*documents.Keyed)
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"todos": todos.Member()}, map[string]contracts.MethodFunction{
				"add": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					// One transaction over two documents: both change together, or neither does.
					id, err := self.Tx(func(tx documents.Tx) (any, error) {
						draft, err := tx.Draft(todos.s)
						if err != nil {
							return nil, err
						}
						id := draft.Get("next").(float64)
						_ = draft.Set("next", id+1)
						_, _ = draft.Array("items").Push(jsonObject("id", id, "text", args.Value("text")))
						if note, ok := args.Get("note"); ok && note != nil {
							noteDraft, err := tx.Draft(notes, fmt.Sprint(int(id)))
							if err != nil {
								return nil, err
							}
							_ = noteDraft.Set("body", note)
						}
						return id, nil
					}, ctx)
					return id, err
				}),
				"note": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					body, err := notes.Get(ctx, fmt.Sprint(int(args.Value("id").(float64))))
					if err != nil || body == nil {
						return "(none)", err
					}
					return body.Value("body"), nil
				}),
				"clear": exMethod(func(_ *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					if err := todos.Retire(ctx); err != nil {
						return nil, err
					}
					keys := notes.Keys(ctx)
					defer keys.Close()
					for {
						key, ok, err := keys.Next(ctx)
						if err != nil {
							return nil, err
						}
						if !ok {
							return nil, nil
						}
						if err := notes.Retire(ctx, key); err != nil {
							return nil, err
						}
					}
				}),
			}), nil
		}, ServeOptions{})
	})
	todosRef := exGet(t, host, store, jsonObject())
	first := exCall(t, todosRef, "add", jsonObject("text", "buy milk", "note", "the oat one"))
	exCall(t, todosRef, "add", jsonObject("text", "call Anna"))
	exEqual(t, "todos", exValue(t, todosRef, "todos").(*delta.JsonObject).Value("items"), exJSON(t, `[{"id":1,"text":"buy milk"},{"id":2,"text":"call Anna"}]`))
	exEqual(t, "first", first, float64(1))
	exEqual(t, "note", exCall(t, todosRef, "note", jsonObject("id", first)), "the oat one")
	exCall(t, todosRef, "clear", nil)
	exEqual(t, "after clear", exValue(t, todosRef, "todos"), exJSON(t, `{"items":[],"next":1}`))
	exEqual(t, "note after clear", exCall(t, todosRef, "note", jsonObject("id", first)), "(none)")
	exCloseHost(t, host)
}
