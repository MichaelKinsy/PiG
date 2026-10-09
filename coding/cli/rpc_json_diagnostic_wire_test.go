package cli

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Pi rpc-mode.ts:755-771 forwards the V8 diagnostic through JSON.stringify. Compare wire bytes, not Go-decoded strings, which erase lone surrogate identity.
func TestRPCJSONDiagnosticWireKeepsLoneSurrogate(t *testing.T) {
	inputs := []string{"😀", `\ud800`, `["abcdefg😀",    tru!]`, "tru!xxxxxxxx😀" + strings.Repeat(" ", 20)}
	// Probe both sides of V8's ten-code-unit excerpt boundary without deriving expectations from the Go scanner.
	for _, token := range []string{"!", "😀"} {
		for _, offset := range []int{0, 1, 9, 10, 11, 19, 20, 21} {
			inputs = append(inputs, strings.Repeat(" ", offset)+token+strings.Repeat(" ", 25))
		}
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	oracle := exec.CommandContext(t.Context(), "node", "-e", `process.stdout.write(JSON.stringify(JSON.parse(process.argv[1]).map(input => { try { JSON.parse(input); throw new Error("valid fixture"); } catch (error) { return {type:"response",command:"parse",success:false,error:"Failed to parse command: "+error.message}; } })))`, "--", string(data))
	output, err := oracle.Output()
	if err != nil {
		t.Fatal(err)
	}
	var wants []json.RawMessage
	if err := json.Unmarshal(output, &wants); err != nil {
		t.Fatal(err)
	}
	if len(wants) != len(inputs) {
		t.Fatalf("oracle returned %d rows for %d inputs", len(wants), len(inputs))
	}
	for i, input := range inputs {
		t.Run(input, func(t *testing.T) {
			var value any
			parseErr := json.Unmarshal([]byte(input), &value)
			if parseErr == nil {
				t.Fatal("fixture is valid JSON")
			}
			var wire bytes.Buffer
			writeJSONLine(&wire, rpcParseError([]byte(input), parseErr))
			if got := bytes.TrimSuffix(wire.Bytes(), []byte{'\n'}); !bytes.Equal(got, wants[i]) {
				t.Fatalf("wire=%s\nPi=%s", got, wants[i])
			}
		})
	}
}
