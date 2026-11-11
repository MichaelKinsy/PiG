package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// listKind is the app's host kind: a todo list, a worker process of its own with its own store (`List` of api/v1.ts).
var listKind = func() *contracts.HostKind {
	kind, err := contracts.NewHostKind("list", contracts.Prop("id", contracts.Str()))
	if err != nil {
		panic(err)
	}
	return kind
}()

// item is a todo: `Item` of the lists plugin's contract.ts.
var item = contracts.Obj(
	contracts.Prop("id", contracts.Int()), contracts.Prop("text", contracts.Str()), contracts.Prop("done", contracts.Bool()),
	contracts.Prop("by", contracts.Str()), contracts.Prop("at", contracts.Num()),
)

// todos is `Todos` of the lists plugin's contract.ts, declared again with Go's builders: a contract is data, so the same name,
// version and schema bind. Its objects live at a list's address, on that list's worker.
var todos = func() *contracts.Contract {
	notice := contracts.Union([]contracts.Schema{surfaces.MessageSchema, contracts.Nul()})
	state := []contracts.Property{contracts.Prop("items", contracts.Arr(item)), contracts.Prop("notice", notice)}
	add := contracts.Meth("add", contracts.NewMethod(contracts.Str()))
	tick := contracts.Meth("tick", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Int()), contracts.Prop("done", contracts.Bool()))))
	contract, err := contracts.Remote("todo.todos", contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("list", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			return listKind.Address(delta.JsonObjectOf("id", params.Value("list"))), nil
		},
		Variants: []contracts.VariantEntry{
			contracts.Var("member", contracts.VariantDeclaration{State: state, Methods: []contracts.MethodEntry{add, tick}}),
			contracts.Var("owner", contracts.VariantDeclaration{State: state, Methods: []contracts.MethodEntry{add, tick, contracts.Meth("clear", contracts.NewMethod())}}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// todo is a todo as the steps read it.
type todo struct {
	ID   int
	Text string
	Done bool
	By   string
}

// show is todos as one line: `[x] milk (owner), [ ] eggs (owner)`.
func show(items []todo) string {
	shown := make([]string, len(items))
	for i, it := range items {
		mark := "[ ]"
		if it.Done {
			mark = "[x]"
		}
		shown[i] = fmt.Sprintf("%s %s (%s)", mark, it.Text, it.By)
	}
	return strings.Join(shown, ", ")
}

// itemsOf is the todos of a `items` state value (or a route's answer), as JSON.
func itemsOf(value any) ([]todo, error) {
	text, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var items []todo
	err = json.Unmarshal(text, &items)
	return items, err
}
