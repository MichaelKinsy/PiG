package testenv

import (
	"bytes"
	"os/exec"
	"testing"
)

// NodeJSON runs body, the statements of an async function that read `input` (JSON.parse of inputJSON) and return a value, under the
// qualified Node and returns JSON.stringify of that value. A caller that compares a Go encoding with Pi's passes its lone UTF-16 surrogates
// as the escape (`\ud800`) and reads the answer the same way: JSON.parse and JSON.stringify keep the unit, where a Go string holds it as
// WTF-8 and encoding/json turns it into U+FFFD.
func NodeJSON(t testing.TB, body string, inputJSON []byte) []byte {
	t.Helper()
	const prelude = `let text = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) text += chunk;
const input = JSON.parse(text);
const result = await (async () => {
`
	const epilogue = `
})();
process.stdout.write(JSON.stringify(result));
`
	//nolint:gosec // G204 (class b): program is the qualified node from RequireQualifiedNode; the script is the test's own constant.
	cmd := exec.CommandContext(t.Context(), RequireQualifiedNode(t), "--input-type=module", "-e", prelude+body+epilogue)
	cmd.Stdin = bytes.NewReader(inputJSON)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi's JSON: %v\n%s", err, &stderr)
	}
	return out
}
