package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// nodeGroupScript runs body in Node against runtime.mjs with the per-group state exported (pig additive (D201)); group names an instance's Host group, "" a member of a process one Host owns.
func nodeGroupScript(t *testing.T, body string) {
	t.Helper()
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { Runtime, busFor, drainFor, xrefRealm } from %q;
import { runWithRuntime } from %q;
// member is a runtime joined to the process (attachConnection registers it) over a connection no Host answers; connected false leaves it without one.
const member = (name, group, connected = true) => {
  const runtime = new Runtime("/ext/" + name + ".mjs", undefined, undefined, { name, group });
  const conn = runtime.attachConnection(new EventEmitter());
  if (connected) runtime.conn = conn;
  return runtime;
};
%s`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String(), (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(filepath.Dir(path), "state.mjs"))}).String(), body)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestSharedNodeCellDrainsOnlyTheClosingHost(t *testing.T) {
	nodeGroupScript(t, `
const a = member("a", "g1");
const b = member("b", "g2");
const c = member("c", "");
assert.notEqual(drainFor("g1"), drainFor("g2"));
assert.notEqual(drainFor("g1"), drainFor(""));
assert.equal(drainFor(undefined), drainFor(""));
assert.ok(drainFor("g1").runtimes.has(a) && !drainFor("g1").runtimes.has(b) && !drainFor("g1").runtimes.has(c));
assert.ok(drainFor("g2").runtimes.has(b) && !drainFor("g2").runtimes.has(a));
a.handleNotify({ method: "runtime_input_end" });
assert.equal(drainFor("g1").inputEnded, true, "the closing Host's drain sees its input end");
assert.equal(drainFor("g2").inputEnded, false, "another Host's drain is untouched");
assert.equal(drainFor("").inputEnded, false, "the group-less drain is untouched");
`)
}

func TestSharedNodeCellAbortsOnlyTheEmittingHostsRun(t *testing.T) {
	nodeGroupScript(t, `
const a = member("a", "g1");
const a2 = member("a2", "g1");
const b = member("b", "g2");
for (const runtime of [a, a2, b]) runtime.runSignal = { id: 1, controller: new AbortController() };
a.fireAndForget = () => {};
a.abortRun();
assert.equal(a.runSignal.controller.signal.aborted, true);
assert.equal(a2.runSignal.controller.signal.aborted, true, "a member of the same Host shares its run");
assert.equal(b.runSignal.controller.signal.aborted, false, "another Host's run is not aborted");
`)
}

func TestSharedNodeCellHoldsRegistryWritesPerHost(t *testing.T) {
	nodeGroupScript(t, `
const a = member("a", "g1");
const a2 = member("a2", "g1");
const b = member("b", "g2");
a.holdRegistryWrite("p", undefined);
assert.equal(a.registryHolds.get("p"), 1);
assert.equal(a2.registryHolds.get("p"), 1, "a member of the same Host holds the write");
assert.equal(b.registryHolds.get("p"), undefined, "another Host's registry view is not held");
`)
}

func TestSharedNodeCellCrossesPayloadsToTheRequestersHostFirst(t *testing.T) {
	nodeGroupScript(t, `
const b = member("b", "g2");
const a1 = member("a1", "g1");
const a2 = member("a2", "g1", false);
const chosen = runWithRuntime(a2, () => xrefRealm.transport());
assert.equal(chosen, a1.xrefTransport, "a member without a connection crosses to its own Host's connection, not another Host's");
assert.notEqual(busFor("g1"), busFor("g2"));
`)
}
