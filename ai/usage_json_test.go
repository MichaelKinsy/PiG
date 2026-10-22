package ai

import (
	"encoding/json"
	"testing"
)

// JSON.stringify(message.usage) writes the stored totalTokens (utils/event-stream.ts and the providers set it; a provider that reports no total
// leaves 0, as in Pi's google and openai-responses). A zero total stays 0 on the wire: PiG did not write the counter sum in its place.
func TestUsageMarshalJSONKeepsTheStoredTotal(t *testing.T) {
	for _, test := range []struct {
		usage Usage
		want  string
	}{
		{Usage{Input: 3, Output: 4, CacheRead: 5, CacheWrite: 6}, `"totalTokens":0`},
		{Usage{Input: 3, Output: 4, TotalTokens: 99}, `"totalTokens":99`},
		{Usage{}, `"totalTokens":0`},
	} {
		encoded, err := json.Marshal(test.usage)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonContains(string(encoded), test.want) {
			t.Errorf("Marshal(%+v) = %s, want %s", test.usage, encoded, test.want)
		}
	}
}

// types.ts Usage.cost.totalFromProvider: absent in a usage recorded before the field existed, written after total otherwise, and kept as read.
func TestUsageCostTotalFromProviderWire(t *testing.T) {
	for _, test := range []struct {
		name, wire string
		want       *bool
	}{
		{"absent", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`, nil},
		{"false", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0,"totalFromProvider":false}}`, new(false)},
		{"true", `{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.5,"totalFromProvider":true}}`, new(true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var usage Usage
			if err := json.Unmarshal([]byte(test.wire), &usage); err != nil {
				t.Fatal(err)
			}
			if got := usage.Cost.TotalFromProvider; (got == nil) != (test.want == nil) || (got != nil && *got != *test.want) {
				t.Fatalf("totalFromProvider = %v, want %v", got, test.want)
			}
			encoded, err := json.Marshal(usage)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.wire {
				t.Fatalf("re-encoded %s, want %s", encoded, test.wire)
			}
		})
	}
}

func jsonContains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
