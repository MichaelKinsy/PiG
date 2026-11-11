package host

// Pi's contract.ts DescriptionSchema (Description = Static<typeof DescriptionSchema>) checked with schema.ts isValid under Node 24
// over the descriptions in testdata/pi_description.json (testdata/pi_description.mjs regenerates it from the pinned mirror): a
// valid description of each shape and seeded edits of them. Each case is JSON text and the verdict Pi returned; the Go code
// gets the same text, so descriptionSchema admits exactly the descriptions Pi's schema admits.

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDescriptionSchemaMatchesPi(t *testing.T) {
	data, err := os.ReadFile("testdata/pi_description.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Text   string `json:"text"`
			Expect bool   `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	valid := 0
	for _, item := range oracle.Cases {
		if item.Expect {
			valid++
		}
		if got := assertValid("input", "description", descriptionSchema, decodeChordJSON(t, []byte(item.Text))) == nil; got != item.Expect {
			t.Errorf("isValid(DescriptionSchema, %s) = %v, Pi %v", item.Text, got, item.Expect)
		}
	}
	if len(oracle.Cases) < 1000 || valid < 100 || valid > len(oracle.Cases)-100 {
		t.Fatalf("Pi oracle has %d cases, %d valid", len(oracle.Cases), valid)
	}
}
