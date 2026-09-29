package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// These nine tests mirror packages/ai/test/validation.test.ts in Pi 0.87.1.
// Go never generates JavaScript functions, so the CSP case exercises the same
// validator path as every other call, with no Function-constructor dependency.
func TestValidateToolArgumentsWithoutFunctionConstructor(t *testing.T) {
	assertToolArguments(t, `{"~kind":"Object","type":"object","properties":{"count":{"~kind":"Number","type":"number"}},"required":["count"]}`, `{"count":"42"}`, `{"count":42}`)
}

func TestValidateToolArgumentsPlainJSONPrimitiveRules(t *testing.T) {
	for _, tc := range []struct{ schema, input, want string }{
		{`{"type":"number"}`, `"42"`, `42`}, {`{"type":"number"}`, `true`, `1`}, {`{"type":"number"}`, `null`, `0`},
		{`{"type":"integer"}`, `"42"`, `42`}, {`{"type":"boolean"}`, `"true"`, `true`}, {`{"type":"boolean"}`, `"false"`, `false`},
		{`{"type":"boolean"}`, `1`, `true`}, {`{"type":"boolean"}`, `0`, `false`},
		{`{"type":"string"}`, `null`, `""`}, {`{"type":"string"}`, `true`, `"true"`},
		{`{"type":"null"}`, `""`, `null`}, {`{"type":"null"}`, `0`, `null`}, {`{"type":"null"}`, `false`, `null`},
		{`{"type":["number","string"]}`, `"1"`, `"1"`}, {`{"type":["boolean","number"]}`, `"1"`, `1`},
	} {
		t.Run(tc.schema+tc.input, func(t *testing.T) { assertToolArgumentValue(t, tc.schema, tc.input, tc.want) })
	}
}

func TestValidateToolArgumentsOptionalNullIsOmission(t *testing.T) {
	assertToolArguments(t, `{"~kind":"Object","type":"object","properties":{
  "path":{"~kind":"String","type":"string"},"offset":{"~kind":"Number","type":"number"},
  "nullable":{"~kind":"Union","anyOf":[{"~kind":"String","type":"string"},{"~kind":"Null","type":"null"}]},
  "metadata":{"~kind":"Object","type":"object","properties":{"enabled":{"~kind":"Boolean","type":"boolean"}}}
 },"required":["path","metadata"]}`, `{"path":"file.txt","offset":null,"nullable":null,"metadata":{"enabled":null}}`, `{"path":"file.txt","nullable":null,"metadata":{}}`)
}

func TestValidateToolArgumentsPreservesReferencedNullableOptional(t *testing.T) {
	assertToolArguments(t, `{"type":"object","properties":{"value":{"$ref":"#/$defs/value"}},"$defs":{"value":{"anyOf":[{"type":"number"},{"type":"null"}]}}}`, `{"value":null}`, `{"value":null}`)
}

func TestValidateToolArgumentsPreservesNullableUnionArm(t *testing.T) {
	assertToolArguments(t, `{"~kind":"Object","type":"object","properties":{"value":{"~kind":"Union","anyOf":[{"~kind":"Number","type":"number"},{"~kind":"Null","type":"null"}]}},"required":["value"]}`, `{"value":null}`, `{"value":null}`)
}

func TestValidateToolArgumentsPreservesNullableOneOfArm(t *testing.T) {
	assertToolArgumentValue(t, `{"oneOf":[{"type":"number"},{"type":"null"}]}`, `null`, `null`)
}

func TestValidateToolArgumentsCoercesNullableUnion(t *testing.T) {
	assertToolArgumentValue(t, `{"anyOf":[{"type":"number"},{"type":"null"}]}`, `"42"`, `42`)
}

func TestValidateToolArgumentsNullableArrayWithItems(t *testing.T) {
	assertToolArgumentValue(t, `{"type":["array","null"],"items":{"type":"string"}}`, `null`, `null`)
}

func TestValidateToolArgumentsRejectsInvalidPlainCoercions(t *testing.T) {
	for _, tc := range []struct{ schema, input string }{
		{`{"type":"boolean"}`, `"1"`}, {`{"type":"boolean"}`, `"0"`}, {`{"type":"null"}`, `"null"`}, {`{"type":"integer"}`, `"42.1"`},
	} {
		_, err := validateToolArgsSchema("echo", argumentValueSchema(tc.schema), json.RawMessage(`{"value":`+tc.input+`}`))
		if err == nil || !strings.Contains(err.Error(), "Validation failed") {
			t.Fatalf("schema=%s input=%s error=%v", tc.schema, tc.input, err)
		}
	}
}

func argumentValueSchema(schema string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"value":` + schema + `},"required":["value"]}`)
}
func assertToolArgumentValue(t *testing.T, schema, input, expected string) {
	t.Helper()
	assertToolArguments(t, string(argumentValueSchema(schema)), `{"value":`+input+`}`, `{"value":`+expected+`}`)
}
func assertToolArguments(t *testing.T, schema, input, expected string) {
	t.Helper()
	raw := json.RawMessage(input)
	got, err := validateToolArgsSchema("echo", json.RawMessage(schema), raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != input {
		t.Fatalf("mutated original arguments: %s", raw)
	}
	var actual, want any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %s, want %s", got, expected)
	}
}
