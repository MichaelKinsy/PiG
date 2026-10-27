package host

// pi: packages/chord/src/schema.ts
// Pi's schema.ts run under Node 24 on the inputs in testdata/pi_schema.json (testdata/pi_schema.mjs regenerates it from
// the pinned mirror): canonicalJson, hash64, fits, canonicalIdentity and normalize. Each case carries its inputs and
// the value Pi returned; the Go code gets the same inputs.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

type piSchemaOracle struct {
	OrderCases []struct {
		Schema json.RawMessage `json:"schema"`
		Value  json.RawMessage `json:"value"`
		Expect json.RawMessage `json:"expect"`
	} `json:"orderCases"`
	Encodings  []string `json:"encodings"`
	Assertions []struct {
		Schema  json.RawMessage `json:"schema"`
		Value   json.RawMessage `json:"value"`
		OK      bool            `json:"ok"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"assertions"`
	Canonical []struct {
		Text   string `json:"text"`
		Expect string `json:"expect"`
	} `json:"canonical"`
	Hashes []struct {
		Text   string `json:"text"`
		Expect string `json:"expect"`
	} `json:"hashes"`
	Fits []struct {
		Sub    json.RawMessage `json:"sub"`
		Sup    json.RawMessage `json:"sup"`
		Expect bool            `json:"expect"`
	} `json:"fits"`
	Identities []struct {
		Schema json.RawMessage `json:"schema"`
		Value  json.RawMessage `json:"value"`
		Expect string          `json:"expect"`
	} `json:"identities"`
	Normalizes []struct {
		Schema json.RawMessage `json:"schema"`
		What   string          `json:"what"`
		Value  json.RawMessage `json:"value"`
		OK     bool            `json:"ok"`
		Expect json.RawMessage `json:"expect"`
	} `json:"normalizes"`
}

func loadPiSchemaOracle(t *testing.T) piSchemaOracle {
	t.Helper()
	data, err := os.ReadFile("testdata/pi_schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle piSchemaOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// schemaOf reads a schema as TypeBox hands it over: a map of JSON values, or a boolean schema.
func schemaOf(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func decodeChordJSON(t *testing.T, raw []byte) any {
	t.Helper()
	value, err := chordjson.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCanonicalJSONAndHash64MatchPi(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	if len(oracle.Canonical) == 0 || len(oracle.Hashes) == 0 {
		t.Fatal("empty Pi oracle")
	}
	for _, item := range oracle.Canonical {
		if got := CanonicalJSON(decodeChordJSON(t, []byte(item.Text))); got != item.Expect {
			t.Errorf("canonicalJson(%s) = %s, Pi %s", item.Text, got, item.Expect)
		}
	}
	for _, item := range oracle.Hashes {
		if got := Hash64(item.Text); got != item.Expect {
			t.Errorf("hash64(%q) = %s, Pi %s", item.Text, got, item.Expect)
		}
	}
}

func TestFitsMatchesPi(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	if len(oracle.Fits) < 600 {
		t.Fatalf("Pi oracle has %d fits cases", len(oracle.Fits))
	}
	for index, item := range oracle.Fits {
		sub, sup := schemaOf(t, item.Sub), schemaOf(t, item.Sup)
		if got := Fits(sub, sup); got != item.Expect {
			t.Errorf("case %d: fits(%s, %s) = %v, Pi %v", index, item.Sub, item.Sup, got, item.Expect)
		}
	}
}

func TestCanonicalIdentityAndNormalizeMatchPi(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	for _, item := range oracle.Identities {
		schema := asSchema(schemaOf(t, item.Schema))
		if got := CanonicalIdentity(schema, decodeChordJSON(t, item.Value)); got != item.Expect {
			t.Errorf("canonicalIdentity(%s) = %s, Pi %s", item.Value, got, item.Expect)
		}
	}
	for _, item := range oracle.Normalizes {
		schema := asSchema(schemaOf(t, item.Schema))
		got, err := Normalize(schema, decodeChordJSON(t, item.Value), item.What)
		if item.OK {
			if err != nil {
				t.Errorf("normalize(%s) failed: %v", item.Value, err)
			} else if canonical, want := CanonicalJSON(got), CanonicalJSON(decodeChordJSON(t, item.Expect)); canonical != want {
				t.Errorf("normalize(%s) = %s, Pi %s", item.Value, canonical, want)
			}
			continue
		}
		var want struct {
			Message string          `json:"message"`
			Code    ChordErrorCode  `json:"code"`
			Reason  string          `json:"reason"`
			Details json.RawMessage `json:"details"`
		}
		if err := json.Unmarshal(item.Expect, &want); err != nil {
			t.Fatal(err)
		}
		chordError, ok := err.(*ChordError) //nolint:errorlint // normalize returns the ChordError itself
		if !ok {
			t.Errorf("normalize(%s) = %v, Pi threw %s", item.Value, err, want.Message)
			continue
		}
		if chordError.Message != want.Message || chordError.Code != want.Code || chordError.Reason != want.Reason {
			t.Errorf("normalize(%s) threw %q (%s/%s), Pi %q (%s/%s)", item.Value, chordError.Message, chordError.Code, chordError.Reason, want.Message, want.Code, want.Reason)
		}
		if gotDetails, wantDetails := CanonicalJSON(chordError.Details), CanonicalJSON(decodeChordJSON(t, want.Details)); gotDetails != wantDetails {
			t.Errorf("normalize(%s) details %s, Pi %s", item.Value, gotDetails, wantDetails)
		}
	}
}

// assertValid throws invalid with the first three TypeBox errors of the value as details, whole: keyword, schemaPath,
// instancePath, params and message.
func TestAssertValidMatchesPi(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	if len(oracle.Assertions) < 70 {
		t.Fatalf("Pi oracle has %d assertion cases", len(oracle.Assertions))
	}
	for _, item := range oracle.Assertions {
		// The schema is decoded in order too: TypeBox visits the properties of a schema in its declaration order, which a Go
		// map cannot keep.
		err := assertValid("state", "state thing", decodeChordJSON(t, item.Schema), decodeChordJSON(t, item.Value))
		if item.OK {
			if err != nil {
				t.Errorf("assertValid(%s, %s) = %v, Pi accepted it", item.Schema, item.Value, err)
			}
			continue
		}
		chordError, ok := err.(*ChordError) //nolint:errorlint // assertValid returns the ChordError itself
		if !ok {
			t.Errorf("assertValid(%s, %s) = %v, Pi threw %q", item.Schema, item.Value, err, item.Message)
			continue
		}
		if chordError.Message != item.Message {
			t.Errorf("assertValid(%s, %s) message %q, Pi %q", item.Schema, item.Value, chordError.Message, item.Message)
		}
		if got, want := CanonicalJSON(chordError.Details), CanonicalJSON(decodeChordJSON(t, item.Details)); got != want {
			t.Errorf("assertValid(%s, %s) details\n  go %s\n  pi %s", item.Schema, item.Value, got, want)
		}
	}
}

// A schema that arrives in a wire description, from a Node child or in a hold token, is decoded by Description's
// UnmarshalJSON: the errors of its properties must come in the order it declared them, as TypeBox's do. The details are
// compared in order: errors are a list, and each error's members keep Pi's order.
func TestAssertValidOverADecodedDescriptionKeepsTheDeclaredOrderLikePi(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	checked := 0
	for _, item := range oracle.Assertions {
		if item.OK || len(item.Schema) == 0 || item.Schema[0] != '{' {
			continue
		}
		var description Description
		text := `{"name":"t","scope":"local","version":1,"input":` + string(item.Schema) + `,"variants":{"":{}}}`
		if err := json.Unmarshal([]byte(text), &description); err != nil {
			t.Fatal(err)
		}
		err := assertValid("state", "state thing", description.Input, decodeChordJSON(t, item.Value))
		chordError, ok := err.(*ChordError) //nolint:errorlint // assertValid returns the ChordError itself
		if !ok {
			t.Errorf("assertValid(%s, %s) = %v, Pi threw %q", item.Schema, item.Value, err, item.Message)
			continue
		}
		if got, want := OrderedJSON(chordError.Details), OrderedJSON(decodeChordJSON(t, item.Details)); got != want || chordError.Message != item.Message {
			t.Errorf("assertValid(%s, %s) over a decoded description\n  go %s %q\n  pi %s %q", item.Schema, item.Value, got, chordError.Message, want, item.Message)
		}
		checked++
	}
	if checked < 60 {
		t.Fatalf("checked %d decoded schemas", checked)
	}
}

// A decoded description is written back as Pi's JSON.stringify wrote the schema: every member, nested or top-level, in
// the order it was declared in.
func TestADecodedDescriptionIsWrittenBackInDeclaredOrderLikePi(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	if len(oracle.Encodings) < 3 {
		t.Fatalf("Pi oracle has %d encodings", len(oracle.Encodings))
	}
	for _, encoding := range oracle.Encodings {
		var description Description
		text := `{"name":"t","scope":"local","version":1,"input":` + encoding + `,"variants":{"":{"m":{"kind":"method","args":` + encoding + `,"result":` + encoding + `},"s":{"kind":"state","schema":` + encoding + `}}}}`
		if err := json.Unmarshal([]byte(text), &description); err != nil {
			t.Fatal(err)
		}
		written, err := json.Marshal(description)
		if err != nil {
			t.Fatal(err)
		}
		if string(written) != text {
			t.Errorf("description written back as\n  go %s\n  pi %s", written, text)
		}
	}
}

// Normalize returns the input's members in the order they arrived in, then the defaults the schema adds in its declared
// order, as Pi's does (Value.Clone, Value.Default, Value.Clean).
func TestNormalizeOverADecodedDescriptionKeepsPiMemberOrder(t *testing.T) {
	oracle := loadPiSchemaOracle(t)
	if len(oracle.OrderCases) < 5 {
		t.Fatalf("Pi oracle has %d order cases", len(oracle.OrderCases))
	}
	for _, item := range oracle.OrderCases {
		var description Description
		text := `{"name":"t","scope":"local","version":1,"input":` + string(item.Schema) + `,"variants":{"":{}}}`
		if err := json.Unmarshal([]byte(text), &description); err != nil {
			t.Fatal(err)
		}
		got, err := Normalize(description.Input, decodeChordJSON(t, item.Value), "input of thing")
		if err != nil {
			t.Fatalf("normalize(%s): %v", item.Value, err)
		}
		if gotText, want := OrderedJSON(got), OrderedJSON(decodeChordJSON(t, item.Expect)); gotText != want {
			t.Errorf("normalize(%s)\n  go %s\n  pi %s", item.Value, gotText, want)
		}
	}
}
