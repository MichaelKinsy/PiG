package ai_test

// pi: packages/ai/src/utils/validation.ts (Pi b556cfe5: validateToolArguments is pi-schema's compile with coerce, the formats of pi-schema/formats and unresolvedRefs "reject")
//
// The oracle is Pi's own validation.ts, run by Node 24 from .upstream/current (testdata/validate_tool_args.mjs), over corpora of {schema, arguments}: Pig's ValidateToolArgumentsJSON must
// return the same coerced arguments, in the same member order, or fail with the same message. A probe is identical only when the JSON text of the coerced arguments or the error message is byte-identical.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type validateProbe struct {
	Schema string
	Args   string
}

// piProbe is the oracle's input: the tool name, the tool's parameters and the call's arguments as JSON text.
type piProbe struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
	Args   string `json:"args"`
}

// piResult is Pi's answer: the JSON text of the validated arguments, or the error message.
type piResult struct {
	Ok  *string `json:"ok"`
	Err *string `json:"err"`
}

// piValidate runs Pi's validateToolArguments over probes.
func piValidate(t *testing.T, probes []piProbe) []piResult {
	t.Helper()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/validate_tool_args.mjs", testenv.ModuleRoot(t))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle (the mirror .upstream/current must be Pi b556cfe5 or later: make upstream-mirror PI_UPSTREAM_REF=b556cfe5): %v\n%s", err, &stderr)
	}
	var results []piResult
	if err := jsstring.UnmarshalKeepingSurrogates(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(results), len(probes))
	}
	return results
}

// pigValidate is Pig's answer to a probe in the oracle's form.
func pigValidate(probe piProbe) piResult {
	validated, err := ai.ValidateToolArgumentsJSON(probe.Name, json.RawMessage(probe.Schema), json.RawMessage(probe.Args))
	if err != nil {
		message := err.Error()
		return piResult{Err: &message}
	}
	text := string(validated)
	return piResult{Ok: &text}
}

func (r piResult) String() string {
	if r.Err != nil {
		return "ERR " + *r.Err
	}
	if r.Ok == nil {
		return "(none)"
	}
	return *r.Ok
}

// comparePi checks every probe against Pi and reports up to eight differences and how many probes Pi accepted.
func comparePi(t *testing.T, probes []piProbe) (accepted int) {
	t.Helper()
	expected := piValidate(t, probes)
	differing := 0
	for i, probe := range probes {
		want := expected[i]
		if want.Err == nil {
			accepted++
		}
		if got := pigValidate(probe); got.String() != want.String() {
			if differing++; differing <= 8 {
				t.Errorf("schema %s\n args %s\n  Pig %s\n  Pi  %s", probe.Schema, probe.Args, got, want)
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	t.Logf("%d probes identical to Pi (%d accepted, %d rejected)", len(probes)-differing, accepted, len(probes)-accepted)
	return accepted
}

func named(probes []validateProbe) []piProbe {
	out := make([]piProbe, len(probes))
	for i, probe := range probes {
		out[i] = piProbe{"probe", probe.Schema, probe.Args}
	}
	return out
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
		`{"enum":["a","b","c"]}`, `{"enum":[1,2,3]}`, `{"enum":["1","2",null]}`, `{"const":"x"}`, `{"const":1}`, `{"const":true}`, `{"const":null}`, `{"type":"string","enum":["a","b"]}`, `{"type":"boolean","default":false}`, `{}`, `{"type":"string","default":"d"}`, `{"type":"number"}`, `{"type":"integer"}`, `{"type":"boolean"}`, `{"type":"string"}`, `{"type":"null"}`, `{"const":"x","type":"string"}`, `{"const":1,"type":"number"}`, `{"const":true,"type":"boolean"}`, `{"type":"number","minimum":1}`, `{"type":"integer","maximum":5}`,
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
		return `{"type":"array","items":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
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
		out := `{"type":"object","properties":{` + strings.Join(properties, ",") + `}`
		if len(required) > 0 {
			out += `,"required":[` + strings.Join(required, ",") + `]`
		}
		out += g.pick(``, ``, `,"additionalProperties":false`, `,"additionalProperties":true`, `,"additionalProperties":{"type":"string"}`, `,"additionalProperties":{"type":"integer"}`, `,"minProperties":1`, `,"maxProperties":2`,
			`,"patternProperties":{"^a":{"type":"string"}}`, `,"propertyNames":{"pattern":"^[a-c]"}`, `,"dependentRequired":{"a0":["b1"]}`, `,"dependencies":{"a0":["c2"]}`)
		return out + `}`
	case 5:
		out := `{"type":"array","items":` + g.schema(depth-1)
		out += g.pick(``, ``, `,"minItems":1`, `,"maxItems":2`, `,"uniqueItems":true`, `,"contains":{"type":"string"}`, `,"minItems":2,"maxItems":1`)
		return out + `}`
	case 6:
		return `{"anyOf":[` + g.schema(depth-1) + `,` + g.schema(depth-1) + `]}`
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
	// pi-schema's coercion of each schema shape against padded, cased and typed spellings of the values it converts.
	kinds := []string{
		`{"type":"boolean"}`, `{"type":"number"}`, `{"type":"integer"}`, `{"type":"string"}`, `{"type":"null"}`,
		`{"const":true,"type":"boolean"}`, `{"const":1,"type":"number"}`, `{"const":"x","type":"string"}`,
		`{"type":"array","items":{"type":"boolean"}}`, `{"anyOf":[{"type":"number"},{"type":"boolean"}]}`,
		`{"type":"array","items":[{"type":"number"},{"type":"boolean"}]}`,
		`{"type":"object","patternProperties":{"^a":{"type":"number"}}}`,
		`{"type":"object","patternProperties":{"a":{"type":"number"}},"additionalProperties":{"type":"string"}}`,
		`{"type":"object","patternProperties":{"a":{"type":"number"},"b|c":{"type":"boolean"}},"additionalProperties":{"type":"string"}}`,
	}
	convertAtoms := []string{`"TRUE"`, `"True"`, `"true"`, `"FALSE"`, `"False"`, `"1"`, `"0"`, `"-0"`, `" 1 "`, `"1\u00a0"`, `"\ufeff1"`, `"\u00851"`, `"1\u0085"`, `"1.5"`, `"0x1F"`, `"1e3"`, `"1n"`, `"-1n"`, `"NULL"`, `"Null"`, `"null"`,
		`"undefined"`, `"UNDEFINED"`, `"UNDEFİNED"`, `"undefİned"`, `"TRUEİ"`, `"tRuE"`, `"falſe"`, `"x"`, `"X"`, `""`, `null`, `true`, `false`, `1`, `0`, `2`, `1.5`, `[]`, `["1","TRUE"]`, `["TRUE",true]`, `{"a1":"5","b":"TRUE"}`, `{"a":"6","xb":"1","c":"false","b":"TRUE","a1":1}`, `{"xa":1,"ax":2,"xb":1,"zz":true,"a":"6","bx":2,"c":null}`, `{}`}
	for _, kind := range kinds {
		for _, atom := range convertAtoms {
			probes = append(probes, validateProbe{`{"type":"object","properties":{"a":` + kind + `},"required":["a"]}`, `{"a":` + atom + `}`})
		}
	}
	// Member order with integer-like and duplicate keys; propertyNames errors at each member's path; keys with "/", "~" and "" in error paths (pi-schema's error path escapes "~" and "/" in a key, and validation.ts turns the pointer into a dotted path).
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
		// A property name is a name, not a regular expression (TypeBox's Convert read it as `^${name}$`): "a." does not convert "ab", "a+" not "aaa", "a|b" neither "axx" nor "xxb", and additionalProperties converts every key no name equals.
		{`{"type":"object","properties":{"a.":{"type":"number"}}}`, `{"ab":"5","a.":"6"}`},
		{`{"type":"object","properties":{"a+":{"type":"number"}}}`, `{"aaa":"5","a+":"6"}`},
		{`{"type":"object","properties":{"a|b":{"type":"number"}}}`, `{"axx":"5","xxb":"6","a|b":"7"}`},
		{`{"type":"object","properties":{"a.":{"type":"number"},"ab":{"type":"number"}}}`, `{"ab":"5"}`},
		{`{"type":"object","properties":{"x.y":{"type":"string"}},"additionalProperties":{"type":"number"}}`, `{"xzy":1,"x.y":2,"q":"7"}`},
		{`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"additionalProperties":{"type":"string"}}`, `{"a":"1","c":5}`},
		// A format the formats table lacks passes; the JSON Schema library's period and semver checks must not apply.
		{`{"type":"object","properties":{"p":{"type":"string","format":"period"},"s":{"type":"string","format":"semver"},"u":{"type":"string","format":"no-such-format"}}}`, `{"p":"x","s":"1.2","u":"?"}`},
		{`{"type":"object","properties":{"p":{"type":"string","format":"period"},"s":{"type":"string","format":"semver"}},"required":["q"]}`, `{"p":"","s":"v1"}`},
	}...)
}

// Pi's validateToolArguments over seeded schemas of the keywords tool definitions use, and the near-miss arguments models send.
func TestValidateToolArgumentsMatchesPiOnSeededSchemas(t *testing.T) {
	probes := named(validateProbes())
	accepted := comparePi(t, probes)
	if accepted < 400 || accepted > len(probes)-1000 {
		t.Errorf("probes are one-sided: %d accepted of %d", accepted, len(probes))
	}
}

// Corpus (a): every case of Pi's validation-probes.test.ts (testdata/validation-probes.json). Pi's recorded outcome of each is the one the Pi test file asserts, and Pig must also equal Pi's validation of the same JSON.
func TestValidateToolArgumentsMatchesPiOnPiProbes(t *testing.T) {
	rows := loadValidationProbes(t)
	probes := make([]piProbe, len(rows))
	for i, row := range rows {
		probes[i] = piProbe{row.Name, row.Schema, row.Args}
	}
	comparePi(t, probes)
}
