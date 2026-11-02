package extension_test

// pi: packages/coding-agent/src/core/extensions/types.ts

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// types.ts:1159-1163 declares sendMessage's first parameter as Pick<CustomMessage, "customType" | "content" | "display" |
// "details">: an extension passes a JavaScript object, so `details` enumerates its array-index keys first in ascending order and
// its other keys in insertion order (JSON.parse, then JSON.stringify of the details Pi stores on the custom message). The Go
// payload must decode `details` to the same member order, which Go's map-based decoding would not keep. The expected text is
// computed by Node, the engine Pi runs on, over the same inputs.
func TestSendMessagePayloadDetailsEnumerateLikeAJavaScriptObject(t *testing.T) {
	inputs := []string{
		`{"customType":"note","content":"hi","display":true,"details":{"b":1,"2":2,"a":3,"1":4}}`,
		`{"customType":"note","display":false,"details":{"z":{"y":1,"10":2,"9":3},"4294967295":1,"4294967294":2,"01":3}}`,
		`{"customType":"note","content":[{"type":"text","text":"x"}],"display":true,"details":{"c":null,"b":[3,{"2":1,"1":0}],"a":"s"}}`,
	}
	const script = `
const out = JSON.parse(require("fs").readFileSync(0, "utf8")).map((text) => JSON.stringify(JSON.parse(text).details));
process.stdout.write(JSON.stringify(out));`
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Node oracle: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Node oracle output %q: %v", output, err)
	}
	if len(want) != len(inputs) {
		t.Fatalf("Node oracle returned %d results for %d inputs", len(want), len(inputs))
	}
	for i, in := range inputs {
		var payload extension.SendMessagePayload
		if err := json.Unmarshal([]byte(in), &payload); err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
		got, err := json.Marshal(payload.Details)
		if err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
		if string(got) != want[i] {
			t.Errorf("input %d details = %s, want %s (Node)", i, got, want[i])
		}
	}
}

// types.ts:1472 SessionBeforeCompactResult.compaction is a CompactionResult whose `details` is the extension's own JavaScript object
// (core/compaction/compaction.ts:105): decoding the extension's result must enumerate `details` as Node does.
func TestCompactionResultDetailsEnumerateLikeAJavaScriptObject(t *testing.T) {
	inputs := []string{
		`{"summary":"s","firstKeptEntryId":"e1","tokensBefore":10,"details":{"b":1,"2":2,"a":3,"1":4}}`,
		`{"summary":"s","firstKeptEntryId":"e1","tokensBefore":10,"estimatedTokensAfter":4,"details":{"readFiles":["a","b"],"7":{"y":1,"3":2},"modifiedFiles":[]}}`,
	}
	const script = `
const out = JSON.parse(require("fs").readFileSync(0, "utf8")).map((text) => JSON.stringify(JSON.parse(text).details));
process.stdout.write(JSON.stringify(out));`
	input, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Node oracle: %v\n%s", err, stderr.String())
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("Node oracle output %q: %v", output, err)
	}
	if len(want) != len(inputs) {
		t.Fatalf("Node oracle returned %d results for %d inputs", len(want), len(inputs))
	}
	for i, in := range inputs {
		var result extension.CompactionResult
		if err := json.Unmarshal([]byte(in), &result); err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
		got, err := json.Marshal(result.Details)
		if err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
		if string(got) != want[i] {
			t.Errorf("input %d details = %s, want %s (Node)", i, got, want[i])
		}
	}
}
