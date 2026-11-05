package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const pinnedTypeboxVersion = "1.3.27"

// refineSemantics are the named refinements of testdata/refine_oracle.mjs, in the order of its `names`.
var refineNames = []string{"pos", "safe", "even", "nonEmpty", "ok", "bad", "sumLt10", "ab"}

func refineCheck(name string, value any) bool {
	switch name {
	case "pos":
		n, ok := value.(float64)
		return ok && n > 0
	case "safe":
		return value == "safe"
	case "even":
		switch v := value.(type) {
		case []any:
			return len(v)%2 == 0
		case float64:
			return int64(v)%2 == 0
		}
		return false
	case "nonEmpty":
		switch v := value.(type) {
		case []any:
			return len(v) > 0
		case *chordjson.Object:
			return v.Len() > 0
		}
		return false
	case "ok":
		return true
	case "bad":
		return false
	case "sumLt10":
		sum := 0.0
		for _, item := range value.([]any) {
			sum += item.(float64)
		}
		return sum < 10
	case "ab":
		object := value.(*chordjson.Object)
		a, _ := object.Value("a").(float64)
		b, _ := object.Value("b").(float64)
		return a < b
	}
	panic(name)
}

type refineCase struct {
	Schema string `json:"schema"`
	Value  any    `json:"value"`
}

type refineAnswer struct {
	Wire       json.RawMessage   `json:"wire"`
	Check      bool              `json:"check"`
	CheckCalls [][3]any          `json:"checkCalls"`
	Errors     []json.RawMessage `json:"errors"`
	ErrorCalls [][3]any          `json:"errorCalls"`
}

var refineCases = []refineCase{
	// Plain properties: a refinement runs when its node's other keywords pass, in declaration order, and the walk stops at the first failure.
	{`Type.Object({ n: refine(Type.Number(), "pos"), s: Type.String() })`, map[string]any{"n": -1, "s": 1}},
	{`Type.Object({ n: refine(Type.Number(), "pos"), s: Type.String() })`, map[string]any{"n": 3, "s": "x"}},
	{`Type.Object({ s: Type.String(), n: refine(Type.Number(), "pos") })`, map[string]any{"n": -1, "s": 1}},
	{`Type.Object({ a: refine(Type.Number(), "pos"), b: refine(Type.Number(), "even"), c: refine(Type.Number(), "pos") })`, map[string]any{"a": 1, "b": 3, "c": 1}},
	{`Type.Object({ a: refine(Type.Number(), "pos"), b: refine(Type.Number(), "even"), c: refine(Type.Number(), "pos") })`, map[string]any{"c": 5, "b": 2, "a": 1}},
	// Two refinements on one node run in the order they were added; the first failure stops Check and the second still runs for Errors.
	{`refine(refine(Type.Number(), "pos"), "even")`, 3},
	{`refine(refine(Type.Number(), "pos"), "even")`, -4},
	{`refine(refine(Type.Number(), "bad"), "bad")`, 4},
	// A node whose type fails does not run its refinement.
	{`refine(Type.String(), "safe")`, 5},
	{`refine(Type.String(), "safe")`, "unsafe"},
	{`refine(Type.String(), "safe")`, "safe"},
	// A refined object: its refinement runs after its properties.
	{`refine(Type.Object({ a: refine(Type.Number(), "pos"), b: Type.Number() }), "ab")`, map[string]any{"a": 2, "b": 1}},
	{`refine(Type.Object({ a: refine(Type.Number(), "pos"), b: Type.Number() }), "ab")`, map[string]any{"a": 1, "b": 2}},
	{`refine(Type.Object({ a: refine(Type.Number(), "pos"), b: Type.Number() }), "ab")`, map[string]any{"a": 1, "b": "x"}},
	// Arrays and tuples.
	{`Type.Array(refine(Type.Number(), "pos"))`, []any{1, 2, -3, 4}},
	{`refine(Type.Array(Type.Number()), "sumLt10")`, []any{4, 5, 6}},
	{`refine(Type.Array(Type.Number()), "sumLt10")`, []any{4, 5}},
	{`Type.Tuple([refine(Type.Number(), "pos"), refine(Type.String(), "safe")])`, []any{1, "bad"}},
	{`Type.Tuple([refine(Type.Number(), "pos"), refine(Type.String(), "safe")])`, []any{-1, "safe"}},
	{`Type.Array(refine(Type.Number(), "even"), { minItems: 4 })`, []any{2, 3}},
	// Unions, intersections: every operand runs, passing ones count.
	{`Type.Union([refine(Type.Number(), "pos"), refine(Type.String(), "safe")])`, "safe"},
	{`Type.Union([refine(Type.Number(), "pos"), refine(Type.Number(), "even")])`, 4},
	{`Type.Union([refine(Type.Number(), "pos"), refine(Type.Number(), "even")])`, -3},
	{`Type.Intersect([Type.Object({ a: refine(Type.Number(), "pos") }), Type.Object({ b: refine(Type.Number(), "even") })])`, map[string]any{"a": 1, "b": 3}},
	{`Type.Intersect([Type.Object({ a: refine(Type.Number(), "pos") }), Type.Object({ b: refine(Type.Number(), "even") })])`, map[string]any{"a": -1, "b": 3}},
	{`Type.Intersect([Type.Object({ a: refine(Type.Number(), "pos") }), Type.Object({ b: Type.Number() })], { unevaluatedProperties: false })`, map[string]any{"a": 1, "b": 3, "c": 1}},
	{`Type.Intersect([Type.Object({ a: refine(Type.Number(), "pos") }), Type.Object({ b: Type.Number() })], { unevaluatedProperties: refine(Type.Number(), "even") })`, map[string]any{"a": 1, "b": 3, "c": 3, "d": 4}},
	// Optional, Record, additional properties.
	{`Type.Object({ a: Type.Optional(refine(Type.Number(), "pos")) })`, map[string]any{}},
	{`Type.Object({ a: Type.Optional(refine(Type.Number(), "pos")) })`, map[string]any{"a": -1}},
	{`Type.Record(Type.String(), refine(Type.Number(), "pos"))`, map[string]any{"x": 1, "y": -1, "z": 2}},
	{`Type.Object({ a: Type.Number() }, { additionalProperties: refine(Type.Number(), "even") })`, map[string]any{"a": 1, "b": 2, "c": 3, "d": 4}},
	// not, if/then/else, contains.
	{`Type.Unsafe({ not: refine(Type.Number(), "pos") })`, 5},
	{`Type.Unsafe({ not: refine(Type.Number(), "pos") })`, -5},
	{`Type.Object({ a: Type.Unsafe({ "if": { type: "number" }, then: refine(Type.Number(), "pos"), else: refine(Type.String(), "safe") }) })`, map[string]any{"a": -1}},
	{`Type.Object({ a: Type.Unsafe({ "if": { type: "number" }, then: refine(Type.Number(), "pos"), else: refine(Type.String(), "safe") }) })`, map[string]any{"a": "x"}},
	{`Type.Array(Type.Number(), { contains: refine(Type.Number(), "even") })`, []any{1, 3, 4, 6}},
	{`Type.Array(Type.Number(), { contains: refine(Type.Number(), "even") })`, []any{1, 3}},
	{`Type.Array(Type.Number(), { contains: refine(Type.Number(), "even"), minContains: 2, maxContains: 3 })`, []any{2, 4, 6, 8}},
	// not: what the negated schema evaluated while failing counts as evaluated for unevaluatedProperties (Check and Errors agree).
	{`Type.Unsafe({ not: { properties: { a: {} }, minProperties: 5 }, unevaluatedProperties: false })`, map[string]any{"a": 1}},
	// References: a named definition and a cyclic type.
	{`Type.Module({ Node: Type.Object({ n: refine(Type.Number(), "pos"), next: Type.Optional(Type.Ref("Node")) }) }).Node`, map[string]any{"n": 1, "next": map[string]any{"n": -2}}},
	{`Type.Cyclic({ Node: Type.Object({ n: refine(Type.Number(), "pos"), next: Type.Optional(Type.Ref("Node")) }) }, "Node")`, map[string]any{"n": 1, "next": map[string]any{"n": 2, "next": map[string]any{"n": -1}}}},
	// String formats and bounds before the refinement; many errors and the error cap.
	{`Type.Object({ a: refine(Type.String({ minLength: 3 }), "safe"), b: refine(Type.String({ pattern: "^x" }), "safe") })`, map[string]any{"a": "ab", "b": "yy"}},
	{`Type.Object({ a: refine(Type.String({ minLength: 3 }), "safe"), b: refine(Type.String({ pattern: "^x" }), "safe") })`, map[string]any{"a": "abc", "b": "xy"}},
	{`Type.Array(refine(Type.Number(), "bad"))`, []any{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	// Nested objects with several failing members, and a refined root over a failing body.
	{`Type.Object({ a: Type.Object({ b: refine(Type.Number(), "pos"), c: refine(Type.Number(), "even") }), d: refine(Type.Number(), "pos") })`, map[string]any{"a": map[string]any{"b": -1, "c": 1}, "d": -2}},
	{`refine(Type.Object({ a: Type.Number() }), "nonEmpty")`, map[string]any{}},
	{`refine(Type.Object({ a: Type.Optional(Type.Number()) }), "nonEmpty")`, map[string]any{}},
	// A refinement under a null and an absent value; a literal union before it.
	{`Type.Object({ a: refine(Type.Union([Type.Null(), Type.Number()]), "ok") })`, map[string]any{"a": nil}},
	{`Type.Object({ k: Type.Union([Type.Literal("a"), Type.Literal("b")]), n: refine(Type.Number(), "pos") })`, map[string]any{"k": "c", "n": -1}},
}

// TestRefineMatchesTypeBox runs Value.Check and Value.Errors of typebox 1.3.27 and of the host (Serve, answered like an SDK does) over the same cases: the result, the errors with their
// refinement messages, and the sequence of refinement calls with their values must be the same.
func TestRefineMatchesTypeBox(t *testing.T) {
	payload, err := json.Marshal(refineCases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "../testdata/refine_oracle.mjs", testenv.ModuleRoot(t), pinnedTypeboxVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("typebox oracle (npm ci --ignore-scripts in internal/typebox/testdata/npm first): %v\n%s", err, &stderr)
	}
	var answers []refineAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	for index, answer := range answers {
		label := fmt.Sprintf("case %d %s on %v", index, refineCases[index].Schema, refineCases[index].Value)
		value, _ := json.Marshal(refineCases[index].Value)
		gotCheck, gotCalls, _ := serveToCompletion(t, memberValueCheck, answer.Wire, value, oracleAnswer)
		if gotCheck.(bool) != answer.Check {
			t.Errorf("%s: Check %v, typebox %v", label, gotCheck, answer.Check)
		}
		if got, want := callsText(gotCalls), callsText(toCalls(answer.CheckCalls)); got != want {
			t.Errorf("%s: Check calls\n  go %s\n  tb %s", label, got, want)
		}
		gotErrors, gotErrorCalls, _ := serveToCompletion(t, memberValueErrors, answer.Wire, value, oracleAnswer)
		if got, want := compactJSON(t, gotErrors), compactJSON(t, answer.Errors); got != want {
			t.Errorf("%s: Errors\n  go %s\n  tb %s", label, got, want)
		}
		if got, want := callsText(gotErrorCalls), callsText(toCalls(answer.ErrorCalls)); got != want {
			t.Errorf("%s: Errors calls\n  go %s\n  tb %s", label, got, want)
		}
	}
}

type recordedCall struct {
	name, kind string
	value      string
}

func toCalls(raw [][3]any) []recordedCall {
	calls := make([]recordedCall, len(raw))
	for i, entry := range raw {
		text, _ := json.Marshal(entry[2])
		calls[i] = recordedCall{entry[0].(string), entry[1].(string), string(text)}
	}
	return calls
}

func callsText(calls []recordedCall) string {
	rows := make([]string, len(calls))
	for i, call := range calls {
		rows[i] = call.name + "." + call.kind + "(" + call.value + ")"
	}
	return strings.Join(rows, " ")
}

func compactJSON(t *testing.T, value any) string {
	t.Helper()
	var text []byte
	switch typed := value.(type) {
	case json.RawMessage:
		text = typed
	case []json.RawMessage:
		text, _ = json.Marshal(typed)
	default:
		var err error
		if text, err = json.Marshal(value); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := json.Compact(&out, text); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return out.String()
}

// valueReply is one reply of Serve to a Value request.
type valueReply struct {
	Value json.RawMessage `json:"value"`
	Call  *struct {
		Token   string          `json:"token"`
		ID      int             `json:"id"`
		Message bool            `json:"message"`
		Value   json.RawMessage `json:"value"`
	} `json:"call"`
	Error *struct{ Name, Message string } `json:"error"`
}

func serve(t testing.TB, request string) valueReply {
	t.Helper()
	var reply valueReply
	if err := json.Unmarshal(Serve([]byte(request)), &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

// serveToCompletion is what an SDK does: start the walk with the schema and the value, answer each refinement call Serve replies with, sending only the answer and the token, until it
// returns a value. It also returns the number of bytes it sent.
func serveToCompletion(t testing.TB, member string, schema json.RawMessage, value []byte, answer func(name string, message bool, value any) any) (any, []recordedCall, int) {
	t.Helper()
	var calls []recordedCall
	sent := 0
	request := fmt.Sprintf(`{"member":%q,"args":[%s,%s]}`, member, schema, value)
	for round := 0; round < 1_000_000; round++ {
		sent += len(request)
		reply := serve(t, request)
		if reply.Error != nil {
			t.Fatalf("%s: %s: %s", member, reply.Error.Name, reply.Error.Message)
		}
		if reply.Call == nil {
			if member == memberValueCheck {
				var passed bool
				if err := json.Unmarshal(reply.Value, &passed); err != nil {
					t.Fatal(err)
				}
				return passed, calls, sent
			}
			var list []json.RawMessage
			if err := json.Unmarshal(reply.Value, &list); err != nil {
				t.Fatal(err)
			}
			return list, calls, sent
		}
		name := refineNames[reply.Call.ID]
		kind := "check"
		if reply.Call.Message {
			kind = "error"
		}
		calls = append(calls, recordedCall{name, kind, compactJSONT(t, reply.Call.Value)})
		decoded, err := chordjson.Decode(reply.Call.Value)
		if err != nil {
			t.Fatal(err)
		}
		given := answer(name, reply.Call.Message, decoded)
		text, _ := json.Marshal(given)
		request = fmt.Sprintf(`{"member":"Value.Answer","args":[%q,%s]}`, reply.Call.Token, text)
	}
	t.Fatal("Serve kept asking")
	return nil, nil, 0
}

// oracleAnswer answers a refinement call with the semantics of testdata/refine_oracle.mjs.
func oracleAnswer(name string, message bool, value any) any {
	if message {
		text, _ := json.Marshal(value)
		return fmt.Sprintf("%s rejected %s", name, text)
	}
	return refineCheck(name, value)
}

func compactJSONT(t testing.TB, raw json.RawMessage) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return out.String()
}
