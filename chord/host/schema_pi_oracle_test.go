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
		if gotDetails, wantDetails := detailsOf(chordError.Details), detailsOf(decodeChordJSON(t, want.Details)); gotDetails != wantDetails {
			t.Errorf("normalize(%s) details %s, Pi %s", item.Value, gotDetails, wantDetails)
		}
	}
}

// detailsOf is the instancePath and message of each schema error in details. Pi's errors also carry keyword, schemaPath
// and params; internal/typebox.ValueError has only the first two (a numbered divergence is proposed for the rest).
func detailsOf(details any) string {
	var rows []any
	switch typed := details.(type) {
	case []any:
		rows = typed
	default:
		return CanonicalJSON(details)
	}
	projected := make([]any, len(rows))
	for index, row := range rows {
		keys, get, ok := objectEntries(row)
		if !ok {
			projected[index] = row
			continue
		}
		entry := map[string]any{}
		for _, key := range keys {
			if key == "instancePath" || key == "message" {
				entry[key] = get(key)
			}
		}
		projected[index] = entry
	}
	return CanonicalJSON(projected)
}
