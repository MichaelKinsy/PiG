package agent

import (
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

// legacyParseProxyContentIndex is parseProxyContentIndex as it decoded the member into an interface value.
func legacyParseProxyContentIndex(raw json.RawMessage) (int, bool) {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return -1, true
	}
	switch key := value.(type) {
	case float64:
		if key >= 0 && key <= maxProxyArrayIndex && key == math.Trunc(key) {
			return int(key), false
		}
	case string:
		if index, err := strconv.ParseUint(key, 10, 32); err == nil && index <= maxProxyArrayIndex && strconv.FormatUint(index, 10) == key {
			return int(index), false
		}
	}
	return -1, true
}

// The typed decode answers as the interface decode did for every wire spelling of a content index: integers, -0, fractions, exponents, overflow, canonical and non-canonical strings, other JSON kinds and malformed text.
func TestParseProxyContentIndexMatchesInterfaceDecode(t *testing.T) {
	for _, text := range []string{
		``, ` `, `0`, `-0`, `-0.0`, `1`, `17`, `4294967294`, `4294967295`, `4294967296`, `1e3`, `1E2`, `1.5`, `-1`, `1e400`, `-1e400`, `01`, `1.`, `.5`, `+1`, `0x10`, ` 7 `, "\n7\t",
		`"0"`, `"17"`, `"01"`, `"-1"`, `"1.5"`, `"4294967294"`, `"4294967295"`, `"4294967296"`, `""`, `" 1"`, `"1 "`, `"\u0031"`, `"\u00e9"`, `"abc`, `"\ud800"`, "\"\xff\"",
		`true`, `false`, `null`, `{}`, `[]`, `[1]`, `{"a":1}`, `nul`, `tru`, `-`, `1 2`, `1,`, `"1""2"`,
	} {
		gotIndex, gotNot := parseProxyContentIndex(json.RawMessage(text))
		wantIndex, wantNot := legacyParseProxyContentIndex(json.RawMessage(text))
		if gotIndex != wantIndex || gotNot != wantNot {
			t.Errorf("parseProxyContentIndex(%q) = %d, %v; interface decode gives %d, %v", text, gotIndex, gotNot, wantIndex, wantNot)
		}
	}
}
