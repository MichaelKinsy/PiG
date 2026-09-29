package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Pi rpc-mode.ts:755-771 forwards JSON.parse's SyntaxError.message, including UTF-16 positions and V8's object/array-specific EOF diagnostics.
func TestRPCJSONDiagnosticsMatchNode(t *testing.T) {
	inputs := []string{"{\r ", "{\r\n ", "{\n\r ", strings.Repeat(" ", 10) + "!" + strings.Repeat(" ", 15), "", "{", "{ ", `{\"x\"`, `{"x"`, `{"x":`, `{"x":1`, `{"x":1,}`, "[", "[1", "[1,]", "not-json", "undefined", "foo", "x", "{x:1}", `{"x" 1}`, `{"x":tru}`, "true false", "01", "-", "1.", "1e", "1e+", `"abc`, "\"a\nb\"", `"\q"`, `"\uQQQQ"`, "{\n \"😀\": 1,}", `{"longPropertyName":"012345678901234567890123456789","x":tru!}`}
	raw, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `process.stdout.write(JSON.stringify(JSON.parse(process.argv[1]).map(s=>{try{JSON.parse(s);return ''}catch(e){return 'Failed to parse command: '+e.message}})))`, "--", string(raw))
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var wants []json.RawMessage
	if err := json.Unmarshal(output, &wants); err != nil {
		t.Fatal(err)
	}
	for i, input := range inputs {
		t.Run(input, func(t *testing.T) {
			var value any
			err := json.Unmarshal([]byte(input), &value)
			if err == nil {
				t.Fatal("fixture is valid JSON")
			}
			if got := rpcParseError([]byte(input), err).Error; !bytes.Equal(got, wants[i]) {
				t.Fatalf("got %q want %q", got, wants[i])
			}
		})
	}
}
