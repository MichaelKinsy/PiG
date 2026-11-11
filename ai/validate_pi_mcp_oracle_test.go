package ai_test

// pi: packages/ai/src/utils/validation.ts, packages/schema/src/{coerce,resolver,formats}.ts
//
// Corpus (c): the plain JSON Schemas MCP servers declare (no builder, no TypeBox): dangling and external $ref, definitions of drafts 7 and 2020-12, enums, formats (url, json-pointer-uri-fragment and the
// rest), oneOf/anyOf unions, patternProperties, nullable, tuples and recursion, each against the arguments models send for them. Pig must answer as Pi's validateToolArguments does.
// Every schema is an object: Pi's validator cache is a WeakMap keyed by tool.parameters, which throws "Invalid value used as weak map key" for a boolean, and a Go tool's parameters are an object.

import (
	"strconv"
	"strings"
	"testing"
)

func mcpSchemas() []string {
	object := func(properties string, extra ...string) string {
		return `{"type":"object","properties":{` + properties + `}` + strings.Join(extra, "") + `}`
	}
	enum := func(count int) string {
		values := make([]string, count)
		for i := range values {
			values[i] = strconv.Quote("v" + strconv.Itoa(i))
		}
		return `[` + strings.Join(values, ",") + `]`
	}
	return []string{
		object(`"t":{"$ref":"#/definitions/Missing"}`),
		object(`"t":{"$ref":"#/definitions/Missing"}`, `,"required":["t"]`),
		object(`"t":{"$ref":"https://example.com/schemas/t.json"}`),
		object(`"t":{"$ref":"#/$defs/Missing"}`, `,"$defs":{"Other":{"type":"string"}}`),
		object(`"t":{"$ref":"#/properties/u"},"u":{"type":"integer"}`),
		`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"t":{"$ref":"#/definitions/T"}},"required":["t"],"definitions":{"T":{"type":"object","properties":{"path":{"type":"string"},"n":{"type":"integer"}},"required":["path"]}}}`,
		`{"type":"object","properties":{"t":{"$ref":"#/$defs/T"}},"$defs":{"T":{"type":"object","properties":{"path":{"type":"string"},"n":{"type":"integer","minimum":1}},"required":["path"]}}}`,
		`{"type":"object","properties":{"u":{"type":"string","format":"url"},"p":{"type":"string","format":"json-pointer-uri-fragment"},"j":{"type":"string","format":"json-pointer"},"r":{"type":"string","format":"relative-json-pointer"}}}`,
		`{"type":"object","properties":{"e":{"type":"string","format":"email"},"d":{"type":"string","format":"date"},"t":{"type":"string","format":"time"},"dt":{"type":"string","format":"date-time"},"du":{"type":"string","format":"duration"},"h":{"type":"string","format":"hostname"},"i4":{"type":"string","format":"ipv4"},"i6":{"type":"string","format":"ipv6"},"uu":{"type":"string","format":"uuid"},"ur":{"type":"string","format":"uri-reference"},"ut":{"type":"string","format":"uri-template"},"x":{"type":"string","format":"unknown-format"}}}`,
		object(`"s":{"type":"string","enum":["a","b","c"]}`, `,"required":["s"]`),
		object(`"n":{"enum":[1,2,3]}`, `,"required":["n"]`),
		object(`"m":{"enum":["a",1,true,null,{"x":1},[1,2]]}`),
		object(`"e":{"type":"string","enum":` + enum(30) + `}`),
		object(`"v":{"oneOf":[{"type":"string"},{"type":"number"}]}`, `,"required":["v"]`),
		object(`"v":{"oneOf":[{"type":"integer"},{"type":"string","maxLength":2}]}`),
		object(`"v":{"anyOf":[{"type":"number"},{"type":"boolean"},{"type":"null"}]}`),
		object(`"v":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]}`, `,"required":["v"]`),
		object(`"v":{"anyOf":[{"type":"object","properties":{"k":{"const":"a"},"n":{"type":"integer"}},"required":["k"]},{"type":"object","properties":{"k":{"const":"b"},"s":{"type":"string"}},"required":["k"]}]}`),
		object(`"v":{"oneOf":[{"type":"object","properties":{"k":{"const":"a"},"n":{"type":"integer"}},"required":["k"],"additionalProperties":false},{"type":"object","properties":{"k":{"const":"b"},"s":{"type":"string"}},"required":["k"],"additionalProperties":false}]}`),
		object(``, `,"patternProperties":{"^n_":{"type":"integer"},"^s_":{"type":"string"}}`),
		object(`"n_fixed":{"type":"string"}`, `,"patternProperties":{"^n_":{"type":"integer"}},"additionalProperties":false`),
		object(``, `,"patternProperties":{"^x-":{"type":"boolean"}},"additionalProperties":{"type":"number"}`),
		object(`"s":{"type":"string","nullable":true}`, `,"required":["s"]`),
		object(`"s":{"type":["string","null"]}`, `,"required":["s"]`),
		object(`"s":{"type":["integer","string"]}`),
		object(`"s":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null}`),
		`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"p":{"type":"array","items":[{"type":"number"},{"type":"string"}],"additionalItems":false}}}`,
		`{"type":"object","properties":{"p":{"type":"array","prefixItems":[{"type":"number"},{"type":"string"}],"items":false}}}`,
		`{"type":"object","properties":{"p":{"type":"array","prefixItems":[{"type":"number"}],"items":{"type":"boolean"}}}}`,
		`{"type":"object","properties":{"t":{"$ref":"#/$defs/Tree"}},"$defs":{"Tree":{"type":"object","properties":{"name":{"type":"string"},"kids":{"type":"array","items":{"$ref":"#/$defs/Tree"}}},"required":["name"]}}}`,
		`{"type":"object","properties":{"t":{"$ref":"#/$defs/Json"}},"required":["t"],"$defs":{"Json":{"anyOf":[{"type":"string"},{"type":"number"},{"type":"boolean"},{"type":"array","items":{"$ref":"#/$defs/Json"}},{"type":"object","additionalProperties":{"$ref":"#/$defs/Json"}}]}}}`,
		`{"allOf":[{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"boolean"}}}]}`,
		`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"if":{"properties":{"a":{"const":1}},"required":["a"]},"then":{"required":["b"]},"else":{"required":[]}}`,
		`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"dependentRequired":{"a":["b"]}}`,
		`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"dependencies":{"a":["b"]}}`,
		`{"type":"object","propertyNames":{"pattern":"^[a-z_]+$"},"additionalProperties":{"type":"integer"}}`,
		`{"type":"object","properties":{"s":{"type":"string","minLength":2,"maxLength":4,"pattern":"^[a-z]+$"},"n":{"type":"number","minimum":0,"exclusiveMaximum":10,"multipleOf":0.5},"l":{"type":"array","items":{"type":"integer"},"minItems":1,"maxItems":3,"uniqueItems":true}}}`,
		`{"type":"object","properties":{"a":true,"b":false,"c":{}}}`,
		`{"type":"object","properties":{"n":{"type":"number"}},"additionalProperties":false,"required":["n"]}`,
		`{"type":"object","properties":{"id":{"type":"string"}},"unevaluatedProperties":false}`,
		`{"$schema":"http://json-schema.org/draft-04/schema#","id":"http://x/y","type":"object","properties":{"n":{"type":"number","minimum":0,"exclusiveMinimum":true}}}`,
		`{"type":"object","properties":{"o":{"type":"object","properties":{"in":{"type":"object","properties":{"n":{"type":"integer"},"s":{"type":"string"}},"required":["n"]}}}}}`,
		`{"type":"object","properties":{"s":{"type":"string","pattern":"(?<!x)y"}}}`,
		`{"type":"object","properties":{"s":{"type":"string","pattern":"["}}}`,
		`{}`,
		`{"type":"string"}`,
		`{"type":"array","items":{"type":"integer"}}`,
	}
}

func mcpArguments() []string {
	return []string{
		`{}`, `null`, `[]`, `"x"`, `7`, `{"t":1}`, `{"t":{"path":"a"}}`, `{"t":{"path":3,"n":"4"}}`, `{"t":{"n":2}}`, `{"u":1,"t":null}`,
		`{"u":"5"}`, `{"u":"https://example.com/a?b=c#d"}`, `{"u":"not a url"}`, `{"u":"example.com"}`, `{"u":"ftp://h/p"}`, `{"u":""}`,
		`{"p":"#/a/b"}`, `{"p":"#"}`, `{"p":"/a/b"}`, `{"p":"#/a%20b"}`, `{"p":"#/a~1b"}`, `{"p":"#/a~2b"}`, `{"p":"#/%zz"}`,
		`{"j":"/a/b"}`, `{"j":"a/b"}`, `{"j":""}`, `{"r":"1/a"}`, `{"r":"0#"}`, `{"r":"x"}`,
		`{"e":"a@b.co"}`, `{"e":"nope"}`, `{"d":"2024-02-30"}`, `{"d":"2024-02-29"}`, `{"t":"12:00:00Z"}`, `{"t":"25:00:00Z"}`, `{"dt":"2024-01-01T00:00:00Z"}`, `{"dt":"2024-01-01"}`, `{"du":"P1D"}`, `{"du":"1D"}`,
		`{"h":"a.b.c"}`, `{"h":"a_b"}`, `{"i4":"1.2.3.4"}`, `{"i4":"1.2.3"}`, `{"i6":"::1"}`, `{"i6":"::g"}`, `{"uu":"00000000-0000-0000-0000-000000000000"}`, `{"uu":"x"}`, `{"ur":"/a/b"}`, `{"ut":"/a{b}"}`, `{"x":"anything"}`,
		`{"s":"a"}`, `{"s":"z"}`, `{"s":1}`, `{"s":null}`, `{"s":""}`, `{"s":true}`, `{"s":["a"]}`, `{"s":"a","extra":1}`, `{"s":"2"}`,
		`{"n":1}`, `{"n":"2"}`, `{"n":4}`, `{"n":"x"}`, `{"n":null}`, `{"n":1.5}`, `{"n":"1.5"}`, `{"n":true}`, `{"n":[1]}`, `{"n":0}`,
		`{"m":"a"}`, `{"m":1}`, `{"m":"1"}`, `{"m":true}`, `{"m":"true"}`, `{"m":null}`, `{"m":{"x":1}}`, `{"m":[1,2]}`, `{"m":[1,3]}`,
		`{"e":"v0"}`, `{"e":"v29"}`, `{"e":"v30"}`, `{"e":"v1x"}`,
		`{"v":"a"}`, `{"v":1}`, `{"v":"1"}`, `{"v":1.5}`, `{"v":true}`, `{"v":null}`, `{"v":["a"]}`, `{"v":[1]}`, `{"v":{}}`, `{"v":{"k":"a","n":"3"}}`, `{"v":{"k":"b","s":4}}`, `{"v":{"k":"c"}}`, `{"v":"abc"}`, `{"v":"ab"}`,
		`{"n_a":"1","s_b":2,"other":3}`, `{"n_a":"x"}`, `{"n_fixed":3,"n_other":"4"}`, `{"x-a":"true","y":"2","x-b":1}`, `{"x-a":"maybe"}`,
		`{"p":[1,"a"]}`, `{"p":["1",2]}`, `{"p":[1,"a",3]}`, `{"p":[1]}`, `{"p":"7"}`, `{"p":[1,"true"]}`, `{"p":[1,true,"false",0]}`,
		`{"t":{"name":"a","kids":[{"name":"b"},{"name":3,"kids":[]}]}}`, `{"t":{"name":"a","kids":"x"}}`, `{"t":{"kids":[]}}`,
		`{"t":"a"}`, `{"t":5}`, `{"t":[1,"a",[true]]}`, `{"t":{"x":[1,{"y":null}]}}`, `{"t":null}`,
		`{"a":"1","b":"true"}`, `{"a":1,"b":true}`, `{"a":"x"}`, `{"a":1}`, `{"a":"y"}`, `{"a":1,"b":2}`, `{"a":"1","c":"d"}`,
		`{"abc":"1","A":2}`, `{"abc":"x"}`, `{"a_b":1,"1":2}`,
		`{"s":"ab","n":"2.5","l":["1","2"]}`, `{"s":"a","n":-1,"l":[]}`, `{"s":"abcde","n":10,"l":[1,1]}`, `{"s":"AB","n":"0.3","l":[1,2,3,4]}`, `{"l":"7"}`, `{"l":[1,"1"]}`,
		`{"a":1,"b":1,"c":1}`, `{"b":"x"}`, `{"id":"7","other":1}`, `{"id":7}`,
		`{"o":{"in":{"n":"3","s":4}}}`, `{"o":{"in":{"s":"x"}}}`, `{"o":{"in":null}}`, `{"o":{"in":{"n":null}}}`, `{"o":null}`,
		`{"s":"y"}`, `[1,"2"]`, `["a"]`, `1`, `"7"`, `{"s":"\ud800"}`, `{"s":"a\udc00b"}`, `{"u":"https://x/\ud800"}`, `{"é":1,"\ud800":2,"x":"\ud83d\ude00"}`,
	}
}

// Corpus (c): MCP-style plain JSON Schemas against near-miss arguments.
func TestValidateToolArgumentsMatchesPiOnMCPSchemas(t *testing.T) {
	var probes []piProbe
	for _, schema := range mcpSchemas() {
		for _, args := range mcpArguments() {
			probes = append(probes, piProbe{"mcp_tool", schema, args})
		}
	}
	accepted := comparePi(t, probes)
	if accepted < len(probes)/10 || accepted > len(probes)*9/10 {
		t.Errorf("probes are one-sided: %d accepted of %d", accepted, len(probes))
	}
}
