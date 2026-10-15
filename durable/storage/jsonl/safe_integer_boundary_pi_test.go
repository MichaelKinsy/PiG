package jsonl_test

// pi: packages/durable/src/storage/jsonl/storage.ts (isSafeInteger :98 = Number.isSafeInteger; main-operation id validation :132-134)

import (
	"strings"
	"testing"
)

// Number.isSafeInteger accepts |n| <= 2^53-1 and rejects 2^53, fractions and non-numbers; a main-log operation with an unsafe id is JsonlCorruptionError "Invalid <type> write in <description>".
// mutation-checked: widening the bound to `> 1<<53` (accepting 2^53), or dropping the integer check, fails it.
func TestJsonlMainOperationIdMustBeASafeInteger(t *testing.T) {
	commit := func(id string) string {
		return `{"format":1,"type":"commit","seq":1,"writes":[{"type":"conversation","value":{"id":` + id + `}}]}` + "\n"
	}
	for _, test := range []struct {
		id      string
		corrupt bool
	}{
		{"9007199254740991", false},
		{"9007199254740992", true},
		{"-9007199254740991", false},
		{"-9007199254740992", true},
		{"1.5", true},
		{`"1"`, true},
		{"null", true},
	} {
		t.Run(test.id, func(t *testing.T) {
			_, err := openJsonl(writeDirectory(t, map[string]string{"main.jsonl": commit(test.id)}))
			if !test.corrupt {
				if err != nil {
					t.Fatalf("open with id %s: %v, want success", test.id, err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), "Invalid conversation write in ") {
				t.Fatalf("open with id %s error = %v, want Invalid conversation write", test.id, err)
			}
		})
	}
}
