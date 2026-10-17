//go:build !pig_strip_codemode

package coding

import "testing"

// A script's returned value is appended like text(): execute.ts:509 keeps every value but undefined, and valueText
// (execute.ts:251-254) gives JSON.stringify(value), so `return null` prints "null" while `return undefined` prints
// nothing. A JSON-string check that treated the literal null as an empty string dropped it.
func TestCodemodeReturnedNullPrintsNullLikePi(t *testing.T) {
	h := newCodemodeHarness(t, codemodeHarnessOptions{fixtures: []string{"nested-tools"}, activeTools: []string{"codemode"}})
	for _, c := range []struct{ code, want string }{
		{"return null", "null"},
		{`console.log("a"); return null`, "null\n<console_output>\na\n</console_output>"},
		{"return undefined", ""},
		{`return ""`, ""},
		{`return "null"`, "null"},
		{"return [null]", "[null]"},
	} {
		if got := codemodeResultText(t, codemodeRun(t, h, c.code)); got != c.want {
			t.Errorf("%s: output %q, want %q", c.code, got, c.want)
		}
	}
}
