package ai

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

// TestJSDateParseMatchesNode compares jsDateParseIn with the values Node's Date.parse returned for the same strings
// (testdata/js_date_parse/generate.mjs). provider-retry.ts:60, github-copilot.ts:158 and openai-codex-responses.ts:158 read a
// Retry-After header that is not a number through Date.parse, which accepts every form V8's date parser does, not only HTTP-dates.
func TestJSDateParseMatchesNode(t *testing.T) {
	raw, err := os.ReadFile("testdata/js_date_parse/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Input   string   `json:"input"`
		UTC     *float64 `json:"utc"`
		NewYork *float64 `json:"newYork"`
		Kolkata *float64 `json:"kolkata"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) < 1000 {
		t.Fatalf("corpus has %d rows", len(rows))
	}
	for _, zone := range []struct {
		name string
		want func(int) *float64
	}{
		{"UTC", func(i int) *float64 { return rows[i].UTC }},
		{"America/New_York", func(i int) *float64 { return rows[i].NewYork }},
		{"Asia/Kolkata", func(i int) *float64 { return rows[i].Kolkata }},
	} {
		t.Run(zone.name, func(t *testing.T) {
			loc, err := time.LoadLocation(zone.name)
			if err != nil {
				t.Fatal(err)
			}
			for i, row := range rows {
				want := math.NaN()
				if value := zone.want(i); value != nil {
					want = *value
				}
				got := jsDateParseIn(row.Input, loc)
				if got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
					t.Errorf("Date.parse(%q) = %v, want %v", row.Input, got, want)
				}
			}
		})
	}
}
