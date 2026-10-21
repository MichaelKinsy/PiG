package telemetry

import (
	"encoding/json"
	"reflect"
	"testing"
)

// packages/telemetry/src/index.ts:136-143 gives each attribute type its own element type for `values`, `elementValues` and `examples`.
// The Go fields of those arms serialize under upstream's member names and read back into the field of the attribute's type.
func TestAttributeDefinitionArmsRoundTripUnderUpstreamMemberNames(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		definition TelemetryAttributeDefinition
		want       string
	}{
		{"string", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeString, Values: []string{"a", "b"}, Examples: []string{"a"}},
			`{"description":"","type":"string","values":["a","b"],"examples":["a"]}`},
		{"number", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeNumber, NumberValues: []float64{1, 2.5}, NumberExamples: []float64{1}},
			`{"description":"","type":"number","values":[1,2.5],"examples":[1]}`},
		{"boolean", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeBoolean, BooleanValues: []bool{true}, BooleanExamples: []bool{false}},
			`{"description":"","type":"boolean","values":[true],"examples":[false]}`},
		{"string[]", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeStringArray, ElementValues: []string{"x"}, ArrayExamples: [][]string{{"x", "x"}}},
			`{"description":"","type":"string[]","elementValues":["x"],"examples":[["x","x"]]}`},
		{"number[]", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeNumberArray, NumberElementValues: []float64{3}, NumberArrayExamples: [][]float64{{3, 3}}},
			`{"description":"","type":"number[]","elementValues":[3],"examples":[[3,3]]}`},
		{"boolean[]", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeBooleanArray, BooleanElementValues: []bool{true}, BooleanArrayExamples: [][]bool{{true, false}}},
			`{"description":"","type":"boolean[]","elementValues":[true],"examples":[[true,false]]}`},
		{"absent members", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeNumber}, `{"description":"","type":"number"}`},
		{"empty members", TelemetryAttributeDefinition{Type: TelemetryAttributeTypeBooleanArray, BooleanElementValues: []bool{}, BooleanArrayExamples: [][]bool{}},
			`{"description":"","type":"boolean[]","elementValues":[],"examples":[]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := json.Marshal(testCase.definition)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != testCase.want {
				t.Fatalf("definition JSON = %s, want %s", encoded, testCase.want)
			}
			var decoded TelemetryAttributeDefinition
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, testCase.definition) {
				t.Fatalf("decoded = %+v, want %+v", decoded, testCase.definition)
			}

			start := TelemetryStartAttributeDefinition{TelemetryAttributeDefinition: testCase.definition, Required: true}
			encoded, err = json.Marshal(start)
			if err != nil {
				t.Fatal(err)
			}
			if want := testCase.want[:len(testCase.want)-1] + `,"required":true}`; string(encoded) != want {
				t.Fatalf("start definition JSON = %s, want %s", encoded, want)
			}
			var decodedStart TelemetryStartAttributeDefinition
			if err := json.Unmarshal(encoded, &decodedStart); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decodedStart, start) {
				t.Fatalf("decoded start = %+v, want %+v", decodedStart, start)
			}
		})
	}
}

func TestAttributeDefinitionRejectsValuesOfTheWrongElementType(t *testing.T) {
	var definition TelemetryAttributeDefinition
	if err := json.Unmarshal([]byte(`{"description":"","type":"number","values":["a"]}`), &definition); err == nil {
		t.Fatal("a number attribute accepted string values")
	}
}
