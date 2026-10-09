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

func jsonContains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
