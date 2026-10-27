package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsparity"
)

// Call runs the member of TypeBox's `Type` namespace named member on args, as JavaScript would: arguments past the
// end are undefined, and what TypeBox throws comes back as an *Error. The result is the JavaScript value.
func Call(member string, args []any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok {
				err = e
				return
			}
			if _, ok := r.(runtime.Error); ok {
				err = &Error{Name: "InternalError", Message: fmt.Sprint(r)}
				return
			}
			panic(r)
		}
	}()
	fn, ok := members()[member]
	if !ok {
		return nil, &Error{Name: "TypeError", Message: "Type." + member + " is not a function"}
	}
	return fn(&eng{}, args), nil
}

// Has reports whether member is served by the engine.
func Has(member string) bool { _, ok := members()[member]; return ok }

// Members lists the members the engine serves.
func Members() []string {
	table := members()
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

type member func(e *eng, args []any) any

func arg(args []any, i int) any { return elem(args, i) }

func ctxOf(v any) *Obj {
	if o, ok := v.(*Obj); ok {
		return o
	}
	return NewObj()
}

func keysToIndexer(keys any) any {
	literals := []any{}
	for _, left := range asArray(keys, "reduce") {
		if isLiteralValue(left) {
			literals = append(literals, tLiteral(left, nil))
		}
	}
	return tUnion(literals, nil)
}

func indexerOf(v any) any {
	if isArray(v) {
		return keysToIndexer(v)
	}
	return v
}

func invalidArguments() { throwError("Invalid Arguments") }

// members is built on first use: the table is a map of closures that nothing reads until a Type call arrives, and every start of
// the program would otherwise pay its allocations in an initializer.
func members() map[string]member {
	membersOnce.Do(func() { memberTable = buildMembers() })
	return memberTable
}

var (
	membersOnce sync.Once
	memberTable map[string]member
)

func buildMembers() map[string]member {
	return map[string]member{
		"Call": func(e *eng, a []any) any {
			return e.callInstantiate(NewObj(), newState(), arg(a, 0), arg(a, 1))
		},
		"Capitalize": func(e *eng, a []any) any {
			return e.intrinsicAction("Capitalize", arg(a, 0), defaultOptions(arg(a, 1)))
		},
		"Lowercase": func(e *eng, a []any) any { return e.intrinsicAction("Lowercase", arg(a, 0), defaultOptions(arg(a, 1))) },
		"Uncapitalize": func(e *eng, a []any) any {
			return e.intrinsicAction("Uncapitalize", arg(a, 0), defaultOptions(arg(a, 1)))
		},
		"Uppercase": func(e *eng, a []any) any { return e.intrinsicAction("Uppercase", arg(a, 0), defaultOptions(arg(a, 1))) },
		"Conditional": func(e *eng, a []any) any {
			return e.conditionalAction(NewObj(), newState(), arg(a, 0), arg(a, 1), arg(a, 2), arg(a, 3), defaultOptions(arg(a, 4)))
		},
		"Constructor": func(e *eng, a []any) any {
			return tConstructor(arg(a, 0), arg(a, 1), arg(a, 2))
		},
		"ConstructorParameters": func(e *eng, a []any) any { return e.constructorParametersAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Cyclic":                func(e *eng, a []any) any { return tCyclic(arg(a, 0), arg(a, 1), arg(a, 2)) },
		"Dependent":             func(e *eng, a []any) any { return tDependent(arg(a, 0), arg(a, 1), arg(a, 2), arg(a, 3)) },
		"Evaluate":              func(e *eng, a []any) any { return e.evaluateAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Exclude":               func(e *eng, a []any) any { return e.excludeAction(arg(a, 0), arg(a, 1), defaultOptions(arg(a, 2))) },
		"Extract":               func(e *eng, a []any) any { return e.extractAction(arg(a, 0), arg(a, 1), defaultOptions(arg(a, 2))) },
		"Extends":               func(e *eng, a []any) any { return e.extends(arg(a, 0), arg(a, 1), arg(a, 2)) },
		"Function":              func(e *eng, a []any) any { return tFunction(arg(a, 0), arg(a, 1), arg(a, 2)) },
		"Generic":               func(e *eng, a []any) any { return tGeneric(arg(a, 0), arg(a, 1)) },
		"Identifier":            func(e *eng, a []any) any { return tIdentifier(arg(a, 0)) },
		"Index": func(e *eng, a []any) any {
			return e.indexAction(arg(a, 0), indexerOf(arg(a, 1)), defaultOptions(arg(a, 2)))
		},
		"Infer": func(e *eng, a []any) any {
			switch len(a) {
			case 2:
				return tInfer(a[0], a[1])
			case 1:
				return tInfer(a[0], tUnknown(nil))
			}
			invalidArguments()
			return nil
		},
		"InstanceType": func(e *eng, a []any) any { return e.instanceTypeAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Instantiate":  func(e *eng, a []any) any { return e.instantiate(ctxOf(arg(a, 0)), arg(a, 1)) },
		"Interface":    func(e *eng, a []any) any { return e.interfaceAction(arg(a, 0), arg(a, 1), defaultOptions(arg(a, 2))) },
		"KeyOf":        func(e *eng, a []any) any { return e.keyOfAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Mapped": func(e *eng, a []any) any {
			return e.mappedAction(NewObj(), newState(), arg(a, 0), arg(a, 1), arg(a, 2), arg(a, 3), defaultOptions(arg(a, 4)))
		},
		"Module":      func(e *eng, a []any) any { return e.moduleInstantiate(NewObj(), arg(a, 0), defaultOptions(arg(a, 1))) },
		"NonNullable": func(e *eng, a []any) any { return e.nonNullableAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Omit": func(e *eng, a []any) any {
			return e.omitAction(arg(a, 0), indexerOf(arg(a, 1)), defaultOptions(arg(a, 2)))
		},
		"Parameter": func(e *eng, a []any) any {
			switch len(a) {
			case 3:
				return tParameter(a[0], a[1], a[2])
			case 2:
				return tParameter(a[0], a[1], a[1])
			case 1:
				return tParameter(a[0], tUnknown(nil), tUnknown(nil))
			}
			invalidArguments()
			return nil
		},
		"Parameters": func(e *eng, a []any) any { return e.parametersAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Partial":    func(e *eng, a []any) any { return e.partialAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Pick": func(e *eng, a []any) any {
			return e.pickAction(arg(a, 0), indexerOf(arg(a, 1)), defaultOptions(arg(a, 2)))
		},
		"ReadonlyObject": func(e *eng, a []any) any { return e.readonlyObjectAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"ReadonlyType":   func(e *eng, a []any) any { return e.readonlyObjectAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Record":         func(e *eng, a []any) any { return e.recordAction(arg(a, 0), arg(a, 1), defaultOptions(arg(a, 2))) },
		"RecordKey":      func(e *eng, a []any) any { return recordKey(arg(a, 0)) },
		"RecordPattern":  func(e *eng, a []any) any { return recordPattern(arg(a, 0)) },
		"RecordValue":    func(e *eng, a []any) any { return recordValue(arg(a, 0)) },
		"Required":       func(e *eng, a []any) any { return e.requiredAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Rest":           func(e *eng, a []any) any { return tRest(arg(a, 0)) },
		"ReturnType":     func(e *eng, a []any) any { return e.returnTypeAction(arg(a, 0), defaultOptions(arg(a, 1))) },
		"Script": func(e *eng, a []any) any {
			var context, input, options any
			switch len(a) {
			case 2:
				if isString(a[0]) {
					context, input, options = NewObj(), a[0], a[1]
				} else {
					context, input, options = a[0], a[1], NewObj()
				}
			case 3:
				context, input, options = a[0], a[1], a[2]
			case 1:
				context, input, options = NewObj(), a[0], NewObj()
			default:
				invalidArguments()
			}
			var parsed any
			text, isStr := input.(string)
			if !isStr {
				// Parser.Script(input) on a non-string: its tokenizers call string methods on it.
				throw("TypeError", "input.trimStart is not a function")
			}
			if r := scriptParse(text); r.ok {
				parsed = e.instantiateType(ctxOf(context), newState(), r.value)
			} else {
				parsed = tNever(nil)
			}
			return Update(parsed, nil0(), options)
		},
		"TemplateLiteral": func(e *eng, a []any) any {
			input, options := arg(a, 0), defaultOptions(arg(a, 1))
			var t any
			if s, ok := input.(string); ok {
				r := scriptTemplateLiteralTypes("`" + s + "`")
				if !r.ok {
					unreachable()
				}
				t = e.templateLiteralAction(r.value, NewObj())
			} else {
				t = e.templateLiteralAction(input, NewObj())
			}
			return Update(t, nil0(), options)
		},
		"This":  func(e *eng, a []any) any { return tThis(arg(a, 0)) },
		"Tuple": func(e *eng, a []any) any { return tTuple(arg(a, 0), defaultOptions(arg(a, 1))) },
		"With":  func(e *eng, a []any) any { return withAction(arg(a, 0), arg(a, 1)) },
	}
}

// Serve answers one engine request: `{"member": "...", "args": [<wire>, ...]}` with `{"value": <wire>}`, or
// `{"error": {"name": "...", "message": "..."}}` for what TypeBox throws. A request that is not that is an error too.
func Serve(request []byte) []byte {
	var req struct {
		Member string          `json:"member"`
		Args   json.RawMessage `json:"args"`
	}
	fail := func(name, message string) []byte {
		var b []byte
		b = append(b, `{"error":{"name":`...)
		b = jsparity.AppendString(b, name)
		b = append(b, `,"message":`...)
		b = jsparity.AppendString(b, message)
		return append(b, `}}`...)
	}
	if err := json.Unmarshal(request, &req); err != nil {
		return fail("TypeError", "bad engine request: "+err.Error())
	}
	decoded, err := Decode(string(req.Args))
	if err != nil {
		var e *Error
		errors.As(err, &e)
		return fail(e.Name, e.Message)
	}
	args, ok := decoded.([]any)
	if !ok {
		return fail("TypeError", "bad engine request: args is not a list")
	}
	value, err := Call(req.Member, args)
	if err != nil {
		var e *Error
		errors.As(err, &e)
		return fail(e.Name, e.Message)
	}
	return append(append([]byte(`{"value":`), Encode(value)...), '}')
}
