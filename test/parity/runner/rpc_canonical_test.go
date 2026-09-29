//go:build parity

package runner

import "testing"

func TestCanonicalJSONLPreservesWireDifferences(t *testing.T) {
	const reference = `{"type":"tool_execution_end","isError":false,"result":{"content":[{"type":"text","text":"two  spaces\n"}]}}`
	canonical, err := canonicalJSONL(reference)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		equal       bool
	}{
		{"key order", `{"result":{"content":[{"text":"two  spaces\n","type":"text"}]},"isError":false,"type":"tool_execution_end"}`, true},
		{"extra nested error", `{"type":"tool_execution_end","isError":false,"result":{"content":[{"type":"text","text":"two  spaces\n"}],"isError":false}}`, false},
		{"extra null details", `{"type":"tool_execution_end","isError":false,"result":{"content":[{"type":"text","text":"two  spaces\n"}],"details":null}}`, false},
		{"missing field", `{"type":"tool_execution_end","result":{"content":[{"type":"text","text":"two  spaces\n"}]}}`, false},
		{"content whitespace", `{"type":"tool_execution_end","isError":false,"result":{"content":[{"type":"text","text":"two spaces\n"}]}}`, false},
		{"duplicate event", reference + "\n" + reference, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalJSONL(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if (got == canonical) != tc.equal {
				t.Fatalf("comparison hides wire difference: %s", got)
			}
		})
	}
	for _, input := range []string{"", `not json`, `{} {}`, "{}\n\n{}"} {
		if _, err := canonicalJSONL(input); err == nil {
			t.Fatalf("accepted invalid JSONL %q", input)
		}
	}
	a, _ := canonicalJSONL("{\"n\":9007199254740992}\n{\"n\":9007199254740993}")
	b, _ := canonicalJSONL("{\"n\":9007199254740993}\n{\"n\":9007199254740992}")
	if a == b {
		t.Fatal("lost numeric precision or event order")
	}
}
