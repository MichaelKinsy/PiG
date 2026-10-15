package orderedjson

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type fieldsRecord struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitempty"`
	Details    any    `json:"details,omitempty"`
	Count      int    `json:"count"`
}

// referenceUnmarshalFields is UnmarshalFields as it was written before the single pass: decode the struct, decode the members again as raw
// JSON, and replace each named `any` field present with Value of its raw JSON. The single pass must leave every target exactly as this does.
func referenceUnmarshalFields(data []byte, v any, members ...string) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	target := reflect.ValueOf(v)
	if target.Kind() != reflect.Pointer || target.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orderedjson: UnmarshalFields needs a pointer to a struct, got %T", v)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	structure := target.Elem()
	for _, member := range members {
		value, present := raw[member]
		if !present {
			continue
		}
		field, ok := fieldByJSONName(structure, member)
		if !ok || field.Kind() != reflect.Interface || field.NumMethod() != 0 {
			return fmt.Errorf("orderedjson: %s has no `any` field with JSON name %q", structure.Type(), member)
		}
		if decoded := Value(value); decoded == nil {
			field.SetZero()
		} else {
			field.Set(reflect.ValueOf(decoded))
		}
	}
	return nil
}

// fieldsCorpus covers the cases the single pass must keep: members absent, null, scalar, containers, repeated (the last wins), spelled in
// another case or with an escape (encoding/json matches struct fields case-insensitively; the raw lookup does not), and the Kelvin sign and
// long s, which fold to k and s.
func fieldsCorpus(rng *rand.Rand) []string {
	values := []string{`null`, `1`, `-0`, `"s"`, `true`, `{"b":1,"a":2}`, `[3,{"z":1,"y":[]}]`, `{"10":1,"2":2,"x":"\u003c"}`, ` { "q" : 1 } `, `{}`, `[]`,
		`"q\"}\\"`, `{"s":"a\"],{\\\\"}`, ` [ "}" , { "]" : "\\" } ] `, "\t{\n\"data\"\r:\n1 }\n", `1e5`}
	keys := []string{`"data"`, `"details"`, `"Data"`, `"DETAILS"`, `"d\u0061ta"`, `"detai\u006cs"`, `"detail` + "\u017f" + `"`, `"type"`, `"count"`, `"id"`, `"customType"`, `"other"`}
	out := []string{`{}`, `null`, `{"data":{"b":1,"a":2}}`, `{"data":null,"data":{"b":1}}`, `{"data":{"b":1},"data":null}`, `{"Data":{"b":1}}`, `{"data":1,"Data":{"b":1}}`, `{"Data":{"b":1},"data":2}`, `{"count":"x","data":{"a":1}}`, `[1]`, `{"data":{"a":1}`,
		` { "data" : { "a" : "x\"}" } , "details" : "\\" } `, `{"other":"\"data\":1","data":[1]}`, `{"d\\ata":1,"data":2}`, `"x"`}
	for range 3000 {
		var members []string
		for range rng.IntN(6) {
			key := keys[rng.IntN(len(keys))]
			value := values[rng.IntN(len(values))]
			switch key {
			case `"type"`, `"id"`, `"customType"`:
				value = `"v"`
			case `"count"`:
				value = `7`
			}
			members = append(members, key+":"+value)
		}
		out = append(out, "{"+strings.Join(members, ",")+"}")
	}
	return out
}

// The single pass leaves every target as the three passes did, error or not, including when the target already holds values.
func TestUnmarshalFieldsMatchesThreePass(t *testing.T) {
	for _, input := range fieldsCorpus(rand.New(rand.NewPCG(11, 12))) {
		for _, seed := range []fieldsRecord{{}, {Data: "kept", Details: map[string]any{"old": 1.0}, Count: 3}} {
			got, want := seed, seed
			gotErr := UnmarshalFields([]byte(input), &got, "data", "details")
			wantErr := referenceUnmarshalFields([]byte(input), &want, "data", "details")
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("UnmarshalFields(%s) error %v, three passes %v", input, gotErr, wantErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("UnmarshalFields(%s)\n got: %#v\nwant: %#v", input, got, want)
			}
		}
	}
	var wrongField struct {
		Data string `json:"data"`
	}
	for _, input := range []string{`{"data":"x"}`, `{}`} {
		gotErr := UnmarshalFields([]byte(input), &wrongField, "data")
		wantErr := referenceUnmarshalFields([]byte(input), &wrongField, "data")
		if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
			t.Fatalf("UnmarshalFields(%s) into a string field: error %v, three passes %v", input, gotErr, wantErr)
		}
	}
	if err := UnmarshalFields([]byte(`{}`), fieldsRecord{}, "data"); fmt.Sprint(err) != fmt.Sprint(referenceUnmarshalFields([]byte(`{}`), fieldsRecord{}, "data")) {
		t.Fatalf("non-pointer target: %v", err)
	}
}

// A custom entry with a large data object decodes it once: the member is not built as a map[string]any and thrown away, and the record is not
// decoded a second time into a map of raw members.
func TestUnmarshalFieldsDecodesOnce(t *testing.T) {
	var data strings.Builder
	data.WriteString(`{"type":"custom","id":"abc","customType":"ext","data":{`)
	for i := range 200 {
		if i > 0 {
			data.WriteByte(',')
		}
		fmt.Fprintf(&data, `"key%d":{"text":"line %d of the payload","n":%d,"list":[1,2,3]}`, i, i, i)
	}
	data.WriteString(`},"count":1}`)
	input := []byte(data.String())
	allocs := testing.AllocsPerRun(20, func() {
		var record fieldsRecord
		if err := UnmarshalFields(input, &record, "data", "details"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 20 {
		t.Fatalf("UnmarshalFields of a 200-member data object allocates %v times, want at most 20", allocs)
	}
}
