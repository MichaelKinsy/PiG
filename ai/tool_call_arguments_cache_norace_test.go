//go:build !race

package ai

import (
	"strconv"
	"strings"
	"testing"
)

// A finished call's arguments are written into every later request. After the first encoding, ArgumentsJSON of a 200 KB write
// call (the large-write row's follow-up request) returns the same bytes without encoding them again: no allocation and no copy.
func TestToolCallArgumentsJSONEncodesALargeCallOnce(t *testing.T) {
	content := strings.Repeat("\tconst value = \"<tag>\" & `x`;\n", 200<<10/31)
	var call ToolCall
	call.SetStreamingArguments(`{"path":"big.ts","content":` + strconv.Quote(content) + `}`)
	first, err := call.ArgumentsJSON()
	if err != nil {
		t.Fatal(err)
	}
	var again []byte
	allocs := testing.AllocsPerRun(20, func() { again, _ = call.ArgumentsJSON() })
	if allocs != 0 || &again[0] != &first[0] {
		t.Fatalf("ArgumentsJSON of a %d-byte call allocates %.0f objects per call (shared bytes: %v), want 0 (shared)", len(first), allocs, &again[0] == &first[0])
	}
}
