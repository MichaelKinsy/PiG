package ai

// pi: packages/ai/src/utils/validation.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const pinnedTypeboxVersion = "1.3.27"

type validateProbe struct {
	Schema string `json:"schema"`
	Args   string `json:"args"`
}

type validateResult struct {
	Ok  json.RawMessage `json:"ok"`
	Err *string         `json:"err"`
}

// validateSchemaGen draws JSON schemas from the keywords tool definitions use: scalar types and type unions, enum and const, objects with required and additionalProperties, arrays with items and bounds,
// numeric and string bounds, anyOf, oneOf and allOf, nullable properties and defaults.
type validateSchemaGen struct{ random *rand.Rand }

func (g validateSchemaGen) pick(options ...string) string {
	return options[g.random.Intn(len(options))]
}

func (g validateSchemaGen) schema(depth int) string {
	scalars := []string{`{"type":"string"}`, `{"type":"number"}`, `{"type":"integer"}`, `{"type":"boolean"}`, `{"type":"null"}`, `{"type":["string","null"]}`, `{"type":["number","string"]}`, `{"type":["integer","boolean"]}`,
		`{"type":"string","minLength":2}`, `{"type":"string","maxLength":3}`, `{"type":"string","pattern":"^a+$"}`, `{"type":"number","minimum":0}`, `{"type":"number","maximum":10}`, `{"type":"integer","minimum":1,"maximum":5}`,
		`{"enum":["a","b","c"]}`, `{"enum":[1,2,3]}`, `{"enum":["1","2",null]}`, `{"const":"x"}`, `{"const":1}`, `{"const":true}`, `{"const":null}`, `{"type":"string","enum":["a","b"]}`, `{"type":"boolean","default":false}`, `{}`, `{"type":"string","default":"d"}`, `{"~kind":"Number","type":"number"}`, `{"~kind":"Integer","type":"integer"}`, `{"~kind":"Boolean","type":"boolean"}`, `{"~kind":"String","type":"string"}`, `{"~kind":"Null","type":"null"}`, `{"~kind":"Literal","const":"x","type":"string"}`, `{"~kind":"Literal","const":1,"type":"number"}`, `{"~kind":"Literal","const":true,"type":"boolean"}`, `{"~kind":"Number","type":"number","minimum":1}`, `{"~kind":"Integer","type":"integer","maximum":5}`,
		`{"type":"number","multipleOf":2}`, `{"type":"number","exclusiveMinimum":0,"exclusiveMaximum":10}`, `{"type":"string","format":"date-time"}`, `{"type":"string","format":"date"}`, `{"type":"string","format":"time"}`, `{"type":"string","format":"duration"}`, `{"type":"string","format":"email"}`, `{"type":"string","format":"ipv4"}`, `{"type":"string","format":"ipv6"}`, `{"type":"string","format":"iri"}`, `{"type":"string","format":"iri-reference"}`, `{"type":"string","format":"json-pointer"}`, `{"type":"string","format":"json-pointer-uri-fragment"}`, `{"type":"string","format":"relative-json-pointer"}`, `{"type":"string","format":"uri"}`, `{"type":"string","format":"uri-reference"}`, `{"type":"string","format":"uri-template"}`, `{"type":"string","format":"url"}`, `{"type":"string","format":"uuid"}`, `{"type":"string","pattern":"^[a-z]+$","minLength":2}`,
		`{"not":{"type":"string"}}`, `{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"number"}}`, `{"type":["string","number","boolean","null"]}`, `{"type":"integer","enum":[1,2]}`, `{"enum":[true,false]}`, `{"enum":[0,1,"0","1"]}`, `{"type":"boolean","enum":[true]}`}
	if depth <= 0 {
		return scalars[g.random.Intn(len(scalars))]
	}
	switch g.random.Intn(11) {
	case 0, 1:
		return scalars[g.random.Intn(len(scalars))]
	case 9:
		return `{"type":"array","prefixItems":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
	case 10:
		return `{"type":"array"` + g.pick(``, `,"~kind":"Tuple"`) + `,"items":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
	case 2, 3, 4:
		count := 1 + g.random.Intn(3)
		var properties, required []string
		for i := range count {
			name := g.pick("a", "b", "c", "d", "e")
			name = fmt.Sprintf("%s%d", name, i)
			properties = append(properties, fmt.Sprintf("%q:%s", name, g.schema(depth-1)))
			if g.random.Intn(2) == 0 {
				required = append(required, fmt.Sprintf("%q", name))
			}
		}
		out := `{"type":"object"` + g.pick(``, `,"~kind":"Object"`, `,"~kind":"Object"`) + `,"properties":{` + strings.Join(properties, ",") + `}`
		if len(required) > 0 {
			out += `,"required":[` + strings.Join(required, ",") + `]`
		}
		out += g.pick(``, ``, `,"additionalProperties":false`, `,"additionalProperties":true`, `,"additionalProperties":{"type":"string"}`, `,"additionalProperties":{"type":"integer"}`, `,"minProperties":1`, `,"maxProperties":2`,
			`,"patternProperties":{"^a":{"type":"string"}}`, `,"propertyNames":{"pattern":"^[a-c]"}`, `,"dependentRequired":{"a0":["b1"]}`, `,"dependencies":{"a0":["c2"]}`)
		return out + `}`
	case 5:
		out := `{"type":"array"` + g.pick(``, `,"~kind":"Array"`, `,"~kind":"Array"`) + `,"items":` + g.schema(depth-1)
		out += g.pick(``, ``, `,"minItems":1`, `,"maxItems":2`, `,"uniqueItems":true`, `,"contains":{"type":"string"}`, `,"minItems":2,"maxItems":1`)
		return out + `}`
	case 6:
		return `{` + g.pick(``, `"~kind":"Union",`) + `"anyOf":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
	case 7:
		return `{"oneOf":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
	default:
		return `{"allOf":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
	}
}

// validateArgsGen draws argument values around what schemas accept and what models send instead: the right type, stringified numbers and booleans, nulls, empty strings, arrays and objects of wrong shapes, big and
// fractional numbers, and extra members.
func (g validateSchemaGen) value(depth int) string {
	atoms := []string{`"a"`, `"b"`, `"x"`, `"aaa"`, `""`, `"1"`, `"2"`, `"0"`, `"1.5"`, `"-0"`, `"true"`, `"false"`, `"TRUE"`, `"False"`, `"NULL"`, `"Null"`, `"UNDEFİNED"`, `"undefİned"`, `"TRUE\u0085"`, `"\ufefftrue"`, `"falſe"`, `"1\u00a0"`, `"True"`, `"null"`, `"undefined"`, `" "`, `"0x10"`, `"1e3"`, `"Infinity"`, `"9007199254740993"`, `"12345678901234567890"`, `"é"`, `"a@b.co"`, `"a@b"`, `"A.B@EXAMPLE.COM"`, `"a b@c.d"`, `"\"q\"@x.y"`, `"a@[1.2.3.4]"`, `"a@[IPv6:::1]"`, `"a@[ipv6:::F]"`, `"\"é\"@x.y"`, `"é@x.y"`, `"a@éx.y"`, `"\"a\\\u00e9\"@x.y"`, `"https://example.com/a?b=c#d"`, `"http://[::1]:80/"`, `"mailto:a@b"`, `"//host/path"`, `"/rel/path"`, `"a/b"`, `"ftp://u:p@h:21/x"`, `"HTTP://X.Y"`, `"http://a b"`, `"urn:isbn:1"`, `"2024-02-29"`, `"2023-02-29"`, `"2024-13-01"`, `"2024-1-1"`, `"2024-02-29T23:59:59Z"`, `"2024-02-29t23:59:60+01:00"`, `"2024-02-29T10:00:00"`, `"10:00:00Z"`, `"23:59:60Z"`, `"23:59:60+00:01"`, `"10:00:00.123+05:30"`, `"24:00:00Z"`, `"P1Y2M3DT4H5M6S"`, `"P1W"`, `"PT"`, `"P"`, `"P1Y2D"`, `"1.2.3.4"`, `"256.1.1.1"`, `"01.2.3.4"`, `"::1"`, `"2001:db8::1"`, `"::ffff:1.2.3.4"`, `":::1"`, `"/a/b"`, `"/a~0b/c~1d"`, `"/a~2"`, `"#/a/b%20c"`, `"0"`, `"1/a/b"`, `"2#"`, `"01/a"`, `"123e4567-e89b-12d3-a456-426614174000"`, `"123E4567-E89B-12D3-A456-426614174000"`, `"123e4567e89b12d3a456426614174000"`, `"http://exämple.com/ü"`, `"http://x/%zz"`, `"http://x/%41"`, `"{var}"`, `"{+path}/here{?q*}"`, `"{ok"`, `"a{b}c"`, `"ſ"`, `"a@bK"`, `"httpſ://x"`, `"\u212a"`,
		`0`, `1`, `2`, `3`, `-1`, `1.5`, `10`, `11`, `100`, `1e21`, `9007199254740993`, `true`, `false`, `null`}
	if depth <= 0 {
		return atoms[g.random.Intn(len(atoms))]
	}
	switch g.random.Intn(7) {
	case 0, 1, 2:
		return atoms[g.random.Intn(len(atoms))]
	case 3, 4:
		count := g.random.Intn(4)
		var members []string
		for i := range count {
			members = append(members, fmt.Sprintf("%q:%s", fmt.Sprintf("%s%d", g.pick("a", "b", "c", "d", "e"), i), g.value(depth-1)))
		}
		return `{` + strings.Join(members, ",") + `}`
	default:
		count := g.random.Intn(4)
		var items []string
		for range count {
			items = append(items, g.value(depth-1))
		}
		return `[` + strings.Join(items, ",") + `]`
	}
}

func validateProbes() []validateProbe {
	g := validateSchemaGen{rand.New(rand.NewSource(20260953))}
	var probes []validateProbe
	for range 6000 {
		schema := g.schema(3)
		if !strings.HasPrefix(schema, `{"type":"object"`) {
			schema = `{"type":"object","properties":{"a0":` + schema + `},"required":["a0"]}`
		}
		probes = append(probes, validateProbe{Schema: schema, Args: g.value(3)},
			validateProbe{Schema: schema, Args: `{"a0":` + g.value(2) + `,"b1":` + g.value(1) + `,"c2":` + g.value(1) + `}`})
	}
	// TypeBox's Convert only runs through ~kind nodes: each kind against padded, cased and typed spellings of the values it converts.
	kinds := []string{
		`{"~kind":"Boolean","type":"boolean"}`, `{"~kind":"Number","type":"number"}`, `{"~kind":"Integer","type":"integer"}`, `{"~kind":"String","type":"string"}`, `{"~kind":"Null","type":"null"}`,
		`{"~kind":"Literal","const":true,"type":"boolean"}`, `{"~kind":"Literal","const":1,"type":"number"}`, `{"~kind":"Literal","const":"x","type":"string"}`,
		`{"~kind":"Array","type":"array","items":{"~kind":"Boolean","type":"boolean"}}`, `{"~kind":"Union","anyOf":[{"~kind":"Number","type":"number"},{"~kind":"Boolean","type":"boolean"}]}`,
		`{"~kind":"Tuple","type":"array","items":[{"~kind":"Number","type":"number"},{"~kind":"Boolean","type":"boolean"}]}`,
		`{"~kind":"Record","type":"object","patternProperties":{"^a":{"~kind":"Number","type":"number"}}}`,
		`{"~kind":"Record","type":"object","patternProperties":{"a":{"~kind":"Number","type":"number"}},"additionalProperties":{"~kind":"String","type":"string"}}`,
		`{"~kind":"Record","type":"object","patternProperties":{"a":{"~kind":"Number","type":"number"},"b|c":{"~kind":"Boolean","type":"boolean"}},"additionalProperties":{"~kind":"String","type":"string"}}`,
	}
	convertAtoms := []string{`"TRUE"`, `"True"`, `"true"`, `"FALSE"`, `"False"`, `"1"`, `"0"`, `"-0"`, `" 1 "`, `"1\u00a0"`, `"\ufeff1"`, `"\u00851"`, `"1\u0085"`, `"1.5"`, `"0x1F"`, `"1e3"`, `"1n"`, `"-1n"`, `"NULL"`, `"Null"`, `"null"`,
		`"undefined"`, `"UNDEFINED"`, `"UNDEFİNED"`, `"undefİned"`, `"TRUEİ"`, `"tRuE"`, `"falſe"`, `"x"`, `"X"`, `""`, `null`, `true`, `false`, `1`, `0`, `2`, `1.5`, `[]`, `["1","TRUE"]`, `["TRUE",true]`, `{"a1":"5","b":"TRUE"}`, `{"a":"6","xb":"1","c":"false","b":"TRUE","a1":1}`, `{"xa":1,"ax":2,"xb":1,"zz":true,"a":"6","bx":2,"c":null}`, `{}`}
	for _, kind := range kinds {
		for _, atom := range convertAtoms {
			probes = append(probes, validateProbe{`{"type":"object","~kind":"Object","properties":{"a":` + kind + `},"required":["a"]}`, `{"a":` + atom + `}`})
		}
	}
	// Member order with integer-like and duplicate keys; propertyNames errors at each member's path; keys with "/", "~" and "" in error paths (TypeBox appends the raw key and validation.ts turns "/" into ".").
	return append(probes, []validateProbe{
		{`{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":{"type":"string"}}`, `{"b":1,"10":2,"2":3,"a":"x","-1":4,"01":5}`},
		{`{"type":"object","additionalProperties":false,"properties":{"a":{"type":"number"}}}`, `{"z":1,"5":1,"a":1,"1":2}`},
		{`{"type":"object","patternProperties":{"^[0-9]+$":{"type":"string"}}}`, `{"9":1,"3":2,"x":3,"1":4}`},
		{`{"type":"object","propertyNames":{"maxLength":1}}`, `{"bb":1,"22":1,"cc":2,"11":3}`},
		{`{"type":"object","additionalProperties":{"type":"string"}}`, `{"a":1,"b":2,"a":3}`},
		{`{"type":"object","properties":{"x":{"type":"object","additionalProperties":{"type":"string"}}}}`, `{"x":{"q":1,"3":2,"p":3}}`},
		{`{"type":"object","properties":{"x":{"type":"object","propertyNames":{"maxLength":1}}}}`, `{"x":{"qq":1,"a/b":2,"t~":3}}`},
		{`{"type":"object","propertyNames":{"pattern":"^a"}}`, `{"ab":1,"b":2}`},
		{`{"type":"object","properties":{"a/b":{"type":"string"},"t~":{"type":"string"},"~1":{"type":"string"}}}`, `{"a/b":1,"t~":2,"~1":3}`},
		{`{"type":"object","additionalProperties":{"type":"string"}}`, `{"a/b":1,"t~0":2,"":3}`},
		{`{"type":"object","patternProperties":{"/":{"type":"string"}}}`, `{"a/b":1}`},
		{`{"type":"object","additionalProperties":{"type":"string"}}`, `{"/a":1,"":2,"b/":3}`},
		{`{"type":"object","properties":{"x":{"type":"object","additionalProperties":{"type":"string"}}}}`, `{"x":{"":1,"/":2}}`},
		{`{"type":"object","properties":{"o/p":{"type":"object","additionalProperties":{"type":"string"}}}}`, `{"o/p":{"z":1,"1":2}}`},
		{`{"type":"object","propertyNames":{"enum":["a"]}}`, `{"b":1}`},
		{`{"type":"object","properties":{"n":{"type":"number"},"s":{"type":"string"}}}`, `{"s":"x","n":"5","7":1,"b":true}`},
		{`{"type":"array","items":{"type":"object","additionalProperties":{"type":"string"}}}`, `[{"k":1,"2":1},{"z":1,"1":2,"a":3}]`},
		// TypeBox Convert's FromObject tests each property name as new RegExp(`^${name}$`): "a." converts "ab", "a+" converts "aaa", "a|b" is ^a or b$, and additionalProperties skips the keys a name matches.
		{`{"~kind":"Object","type":"object","properties":{"a.":{"~kind":"Number","type":"number"}}}`, `{"ab":"5","a.":"6"}`},
		{`{"~kind":"Object","type":"object","properties":{"a+":{"~kind":"Number","type":"number"}}}`, `{"aaa":"5","a+":"6"}`},
		{`{"~kind":"Object","type":"object","properties":{"a|b":{"~kind":"Number","type":"number"}}}`, `{"axx":"5","xxb":"6","a|b":"7"}`},
		{`{"~kind":"Object","type":"object","properties":{"a.":{"~kind":"Number","type":"number"},"ab":{"~kind":"Number","type":"number"}}}`, `{"ab":"5"}`},
		{`{"~kind":"Object","type":"object","properties":{"x.y":{"~kind":"String","type":"string"}},"additionalProperties":{"~kind":"Number","type":"number"}}`, `{"xzy":1,"x.y":2,"q":"7"}`},
		{`{"~kind":"Object","type":"object","properties":{"a":{"~kind":"Number","type":"number"},"b":{"~kind":"Number","type":"number"}},"additionalProperties":{"~kind":"String","type":"string"}}`, `{"a":"1","c":5}`},
		// typebox's Format.Test passes a format its registry lacks; the JSON Schema library's period and semver checks must not apply.
		{`{"type":"object","properties":{"p":{"type":"string","format":"period"},"s":{"type":"string","format":"semver"},"u":{"type":"string","format":"no-such-format"}}}`, `{"p":"x","s":"1.2","u":"?"}`},
		{`{"type":"object","properties":{"p":{"type":"string","format":"period"},"s":{"type":"string","format":"semver"}},"required":["q"]}`, `{"p":"","s":"v1"}`},
	}...)
}

// validateToolArguments (Pi's validation.ts, typebox 1.3.27 Value.Convert, the JSON-schema coercion pass and Compile/Check/Errors) runs in Node on the same schemas and arguments: Pig's ValidateToolArgumentsSchema must
// return the same coerced arguments, or fail with the same message.
func TestValidateToolArgumentsMatchesPiOnSeededSchemas(t *testing.T) {
	probes := validateProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/validate_tool_args.mjs", root, pinnedTypeboxVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle (npm ci --ignore-scripts in internal/typebox/testdata/npm first): %v\n%s", err, &stderr)
	}
	var expected []validateResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	accepted, differing := 0, 0
	for i, probe := range probes {
		got, err := ValidateToolArgumentsJSON("probe", json.RawMessage(probe.Schema), json.RawMessage(probe.Args))
		want := expected[i]
		if want.Err == nil {
			accepted++
		}
		var gotValue, wantValue any
		same := false
		switch {
		case err != nil && want.Err != nil:
			same = err.Error() == *want.Err
		case err == nil && want.Err == nil:
			if json.Unmarshal(got, &gotValue) == nil && json.Unmarshal(want.Ok, &wantValue) == nil {
				same = reflect.DeepEqual(gotValue, wantValue)
			}
		}
		if !same {
			if differing++; differing <= 8 {
				have := string(got)
				if err != nil {
					have = "ERR " + err.Error()
				}
				wantText := string(want.Ok)
				if want.Err != nil {
					wantText = "ERR " + *want.Err
				}
				t.Errorf("schema %s\n args %s\n  Pig %s\n  Pi  %s", probe.Schema, probe.Args, have, wantText)
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if accepted < 400 || accepted > len(probes)-1000 {
		t.Errorf("probes are one-sided: %d accepted", accepted)
	}
}
