package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// A Node before_provider_headers handler mutates event.headers in place and
// returns nothing (upstream ignores the return value). The runtime sends the
// mutated headers back so the host can apply them.
func TestNodeBeforeProviderHeadersRespondsWithMutatedHeaders(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not found: %v", err)
	}
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	script := fmt.Sprintf(`
import { Runtime } from %q;
const runtime = new Runtime("/ext/headers.mjs");
runtime.on("before_provider_headers", (event) => { event.headers["x-test"] = "traced"; event.headers.drop = null; return "ignored"; });
const sent = [];
runtime.conn = { respond: (id, result, error) => sent.push({ id, result, error }) };
await runtime.handleRequest(7, { method: "event", event: "before_provider_headers", handler_id: 1, args: { type: "before_provider_headers", headers: { keep: "1", drop: "x" } } }, {});
const reply = sent[0];
if (!reply || reply.error || JSON.stringify(reply.result) !== JSON.stringify({ keep: "1", drop: null, "x-test": "traced" })) throw new Error(JSON.stringify(sent));
`, runtimeURL)
	if output, err := exec.Command(node, "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node before_provider_headers contract: %v\n%s", err, output)
	}
}

// ctx.navigateTree sends the options flat beside targetId, the shape the host
// and the Go, Rust and Python SDKs use.
func TestNodeNavigateTreeSendsFlatOptions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not found: %v", err)
	}
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	script := fmt.Sprintf(`
import { Runtime } from %q;
const runtime = new Runtime("/ext/tree.mjs");
const calls = [];
runtime.call = async (method, args) => { calls.push({ method, args }); return { cancelled: false }; };
await runtime.ctx.navigateTree("entry-1", { summarize: true, customInstructions: "focus" });
if (JSON.stringify(calls) !== JSON.stringify([{ method: "navigateTree", args: { summarize: true, customInstructions: "focus", targetId: "entry-1" } }])) throw new Error(JSON.stringify(calls));
`, runtimeURL)
	if output, err := exec.Command(node, "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node navigateTree args: %v\n%s", err, output)
	}
}

// Upstream session_before_compact passes preparation.fileOps as three
// Set<string> values (compaction/utils.ts FileOperations). The wire carries
// arrays, so the Node runtime restores Sets before the handler runs.
func TestNodeSessionBeforeCompactRestoresFileOpsSets(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not found: %v", err)
	}
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	script := fmt.Sprintf(`
import { Runtime } from %q;
const runtime = new Runtime("/ext/compact.mjs");
let seen;
runtime.on("session_before_compact", (event) => { seen = event.preparation; });
runtime.conn = { respond: () => {} };
await runtime.handleRequest(1, { method: "event", event: "session_before_compact", handler_id: 1, args: { type: "session_before_compact", preparation: { firstKeptEntryId: "e1", fileOps: { read: ["a.go"], written: [], edited: ["b.go"] } } } }, {});
const ops = seen.fileOps;
if (!(ops.read instanceof Set) || !(ops.written instanceof Set) || !(ops.edited instanceof Set)) throw new Error("fileOps not Sets: " + JSON.stringify(ops));
if (!ops.read.has("a.go") || ops.written.size !== 0 || !ops.edited.has("b.go") || seen.firstKeptEntryId !== "e1") throw new Error("fileOps content lost");
`, runtimeURL)
	if output, err := exec.Command(node, "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node session_before_compact fileOps: %v\n%s", err, output)
	}
}
