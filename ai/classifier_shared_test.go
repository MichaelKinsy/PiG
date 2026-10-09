package ai

import (
	"encoding/json"
	"testing"
)

// packages/ai/src/api/classifier-shared.ts:30 isRecord: `typeof value === "object" && value !== null && !Array.isArray(value)`.
func TestIsRecordMatchesClassifierShared(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{`{}`, true},
		{`{"a":1}`, true},
		{` {"a":[1]}`, true},
		{`[]`, false},
		{`[{"a":1}]`, false},
		{`null`, false},
		{`"text"`, false},
		{`1`, false},
		{`true`, false},
		{``, false},
		{`{"a":`, false},
	} {
		if got := IsRecord(json.RawMessage(tc.value)); got != tc.want {
			t.Errorf("IsRecord(%q) = %t, want %t", tc.value, got, tc.want)
		}
	}
}
