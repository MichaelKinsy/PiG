//go:build !race && !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

const passthroughWriteSchema = `{"~kind":"Object","type":"object","properties":{"path":{"~kind":"String","type":"string"},"content":{"~kind":"String","type":"string"}},"required":["path","content"]}`

func passthroughWriteArgs(size int) json.RawMessage {
	return json.RawMessage(`{"path":"out.txt","content":"` + strings.Repeat("line of text\\n", size/14) + `"}`)
}

func validateBytesPerRun(t *testing.T, args json.RawMessage) uint64 {
	t.Helper()
	if _, err := ValidateToolArgumentsJSON("write", json.RawMessage(passthroughWriteSchema), args); err != nil {
		t.Fatal(err)
	}
	const runs = 5
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		if _, err := ValidateToolArgumentsJSON("write", json.RawMessage(passthroughWriteSchema), args); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// A 200 KB write call that validation leaves as it is: the arguments are decoded once to be checked, and the model's own
// canonical bytes are handed on rather than re-encoded, so admission allocates about the decoded content and no second copy.
func TestValidateToolArgumentsJSONHandsBackUnchangedCanonicalBytes(t *testing.T) {
	args := passthroughWriteArgs(200 << 10)
	validated, err := ValidateToolArgumentsJSON("write", json.RawMessage(passthroughWriteSchema), args)
	if err != nil {
		t.Fatal(err)
	}
	bytes := validateBytesPerRun(t, args)
	t.Logf("%d-byte write call: %d bytes allocated per validation", len(args), bytes)
	if len(validated) == 0 || &validated[0] != &args[0] {
		t.Fatalf("validated arguments are a new encoding (%d bytes allocated); want the input bytes", bytes)
	}
	// The base re-encoded the call: 8.6x the arguments (1,754,308 bytes for 204,823). What remains is the decode and the schema validator's own string copies.
	if bytes > uint64(len(args))*4 {
		t.Fatalf("validating a %d-byte write call allocates %d bytes, want at most 4x the arguments (decode only)", len(args), bytes)
	}
}

// Arguments validation changed, or whose text JSON.stringify would not write as it is, are still re-encoded.
func TestValidateToolArgumentsJSONReencodesChangedOrNonCanonicalArguments(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"},"s":{"type":"string"},"o":{"type":"string"}}}`)
	for _, tc := range []struct{ in, want string }{
		{`{"n":"3","s":"x"}`, `{"n":3,"s":"x"}`},
		{`{"s":"x","o":null}`, `{"s":"x"}`},
		{`{"n": 3}`, `{"n":3}`},
		{`{"n":3.0}`, `{"n":3}`},
		{`{"s":"\u0041"}`, `{"s":"A"}`},
		{`{"s":"x","s":"y"}`, `{"s":"y"}`},
	} {
		got, err := ValidateToolArgumentsJSON("t", schema, json.RawMessage(tc.in))
		if err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Fatalf("%s validated to %s, want %s", tc.in, got, tc.want)
		}
	}
}
