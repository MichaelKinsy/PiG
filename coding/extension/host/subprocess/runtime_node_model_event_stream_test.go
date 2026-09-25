package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNodeModelEventStreamBoundedFIFOAndTransportErrorShape(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not found: %v", err)
	}
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	script := fmt.Sprintf(`
import { ModelEventStream } from %q;
const stream = new ModelEventStream();
for (let sequence = 0; sequence < 5000; sequence++) stream.push({type:"text_delta", sequence});
stream.push({type:"done", message:{stopReason:"stop"}});
stream.push({type:"text_delta", sequence:"ignored"});
const seen = [];
await Promise.all([0, 1].map(async () => { for await (const event of stream) if (Number.isInteger(event.sequence)) seen.push(event.sequence); }));
seen.sort((a, b) => a - b);
if (seen.length !== 5000 || seen.some((value, index) => value !== index)) throw new Error("lost, duplicated, or reordered events");
if (stream.queue.length !== 0 || stream.queueHead !== 0) throw new Error("drained queue retained storage");
const failed = new ModelEventStream();
failed.fail(new Error("transport boom"), {api:"openai-responses", provider:"conformance", modelId:"transport-error"});
const error = await failed.result();
if (error.role !== "assistant" || error.api !== "openai-responses" || error.provider !== "conformance" || error.model !== "transport-error" || error.stopReason !== "error" || error.errorMessage !== "transport boom" || !(error.timestamp > 0) || error.usage.totalTokens !== 0 || error.usage.cost.total !== 0) throw new Error(JSON.stringify(error));
`, runtimeURL)
	command := exec.Command(node, "--input-type=module", "--eval", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("node model stream contract: %v\n%s", err, output)
	}
}
