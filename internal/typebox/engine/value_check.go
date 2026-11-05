package engine

// pig divergence (D226): Value.Check and Value.Errors of TypeBox 1.3.27 for a schema whose nodes carry `Type.Refine` refinements, served to the Go, Rust, Python and Lisp SDKs.
//
// A refinement is a function of the extension process, so the host walks the schema (typebox.RefineCheck, typebox.RefineErrors) and the extension answers each call of a
// refinement. The request carries the answers the extension has given so far; the host replays them, in the order the walk calls the refinements, and when the walk reaches a call
// with no answer it replies with that call instead of a result. The extension runs the refinement, appends its answer and asks again. The walk is deterministic, so every refinement
// call is made once, in the order TypeBox makes it, and an error one throws never reaches the host (the extension fails the call with it, as a thrown JavaScript error does).
//
//	request  {"member":"Value.Check"|"Value.Errors","args":[<schema wire>,<value wire>,[<answer>,...]]}
//	answer   true|false (the check of a refinement) or a string (its error message)
//	reply    {"value":true|false} for Value.Check; {"value":[{"keyword","schemaPath","instancePath","params","message"},...]} for Value.Errors;
//	         {"call":{"id":N,"message":false|true,"value":<json>}} for the next call the extension must answer: refinement N (the index of its `{"\u0000":"f","i":N}` entry in the schema),
//	         asking for its check (message false) or its error message (message true) on the value.

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// valueMembers are the members of TypeBox's `Value` namespace Serve answers.
const (
	memberValueCheck  = "Value.Check"
	memberValueErrors = "Value.Errors"
)

// needAnswer is the panic that stops a walk at a refinement call that has no answer yet.
type needAnswer struct {
	id      int
	message bool
	value   any
}

// replay answers refinement calls from the answers the extension gave, in call order.
type replay struct {
	answers []any
	next    int
}

func (r *replay) hook(id int, message bool, value any) any {
	if r.next < len(r.answers) {
		answer := r.answers[r.next]
		r.next++
		if message {
			text, _ := answer.(string)
			return text
		}
		passed, _ := answer.(bool)
		return passed
	}
	panic(&needAnswer{id: id, message: message, value: value})
}

// schemaData is a decoded wire schema as the typebox walkers read it: JSON Schema data whose objects keep their member order, plus each node's refinements as the RefineKey list of ids.
func schemaData(value any) any {
	switch v := value.(type) {
	case *Obj:
		keys := v.EnumKeys()
		members := make(map[string]any, len(keys)+1)
		for _, key := range keys {
			members[key] = schemaData(v.props[key].value)
		}
		if refinements, ok := v.Get(RefineName); ok {
			if list, isList := refinements.([]any); isList && len(list) > 0 {
				ids := make([]any, 0, len(list))
				for _, entry := range list {
					if opaque, isOpaque := entry.(*Opaque); isOpaque {
						if id, err := strconv.Atoi(opaque.Raw); err == nil {
							ids = append(ids, float64(id))
						}
					}
				}
				if _, declared := members[typebox.RefineKey]; !declared {
					keys = append(keys, typebox.RefineKey)
				}
				members[typebox.RefineKey] = ids
			}
		}
		return typebox.NewOrdered(keys, members)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = schemaData(item)
		}
		return out
	case *Opaque, *undefined, *big.Int:
		return nil
	}
	return value
}

// RefineName is the hidden member of a schema node that lists its refinements.
const RefineName = "~refine"

// valueData is a decoded wire value as the walkers read it: objects keep the member order they arrived in.
func valueData(value any) any {
	switch v := value.(type) {
	case *Obj:
		keys := v.EnumKeys()
		out := chordjson.NewObject(len(keys))
		for _, key := range keys {
			out.Set(key, valueData(v.props[key].value))
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = valueData(item)
		}
		return out
	case *Opaque, *undefined, *big.Int:
		return nil
	}
	return value
}

// serveValue answers one Value.Check or Value.Errors request (see the file comment).
func serveValue(member string, args []any, fail func(name, message string) []byte) (reply []byte) {
	if len(args) != 3 {
		return fail("TypeError", member+" takes a schema, a value and the answers so far")
	}
	answers, _ := args[2].([]any)
	state := &replay{answers: answers}
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		need, ok := recovered.(*needAnswer)
		if !ok {
			panic(recovered)
		}
		text, err := chordjson.Marshal(need.value)
		if err != nil {
			reply = fail("TypeError", "the value cannot be written as JSON: "+err.Error())
			return
		}
		reply = fmt.Appendf(nil, `{"call":{"id":%d,"message":%t,"value":%s}}`, need.id, need.message, text)
	}()
	schema, value := schemaData(args[0]), valueData(args[1])
	if member == memberValueCheck {
		return fmt.Appendf(nil, `{"value":%t}`, typebox.RefineCheck(schema, value, state.hook))
	}
	list := make([]any, 0)
	for _, failure := range typebox.RefineErrors(schema, value, state.hook) {
		list = append(list, chordjson.ObjectOf(
			"keyword", failure.Keyword,
			"schemaPath", failure.SchemaPath,
			"instancePath", failure.InstancePath,
			"params", failure.ParamsObject(),
			"message", failure.Message,
		))
	}
	text, err := chordjson.Marshal(list)
	if err != nil {
		return fail("TypeError", "the errors cannot be written as JSON: "+err.Error())
	}
	return append(append([]byte(`{"value":`), text...), '}')
}
