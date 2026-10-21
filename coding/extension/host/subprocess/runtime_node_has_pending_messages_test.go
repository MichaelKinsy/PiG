package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's hasPendingMessages is a live getter (agent-session.js:2728, `pendingMessageCount > 0`).
// An extension that queues a steer from an awaited boundary handler reads it before the Host
// pushes the next state snapshot, so the runtime must ask the Host instead of a cached flag.
func TestNodeContextHasPendingMessagesReadsTheHostLive(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const runtime = new Runtime("/ext/pending.mjs");
const reads = [];
// The Host owns the queue, so the cached snapshot still says empty.
runtime.state.hasPendingMessages = false;
runtime.callSync = (method) => { reads.push(method); return { pending: true }; };
assert.equal(runtime.ctx.hasPendingMessages(), true);
assert.deepEqual(reads, ["hasPendingMessages"]);
runtime.callSync = (method) => { reads.push(method); return { pending: false }; };
assert.equal(runtime.ctx.hasPendingMessages(), false);
assert.deepEqual(reads, ["hasPendingMessages", "hasPendingMessages"]);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("live hasPendingMessages: %v\n%s", err, output)
	}
}
