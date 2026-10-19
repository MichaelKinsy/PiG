package subprocess

import (
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Node runtime's session reads after an appended entry cost the new entry
// and not the log: the id index, the branch and the label map extend with the
// append. Entries count the reads of their id, so the cost is exact and does
// not depend on the host's load. The same script holds the observable
// behaviour of the incremental paths against an uncached walk of the log.
func TestNodeRuntimeSessionReadsAfterAppendDoNotRescanTheLog(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(`
import assert from "node:assert/strict";
import { Runtime } from __RUNTIME__;

function newRuntime() {
  const runtime = new Runtime("/ext/branch.mjs");
  runtime.conn = { call: async () => ({}), callSync() { return {}; }, requestState() {}, notify() {} };
  runtime.fireAndForget = () => {};
  runtime.sessionLogRequested = true;
  return runtime;
}

let reads = 0;
function entry(id, parentId, extra = {}) {
  const e = { type: "message", parentId, timestamp: "2026-10-11T00:00:00.000Z", message: { role: "user", content: "x" }, ...extra };
  Object.defineProperty(e, "id", { get() { reads++; return id; }, enumerable: true });
  return e;
}
const idOf = (e) => Object.getOwnPropertyDescriptor(e, "id").get();

// push applies one host page: entries appended below the log's current size, with the leaf after them.
function push(runtime, session, entries, count, leafId) {
  runtime.applyState({ session: { sessionId: session, leafId, entryCount: count, entriesAppended: entries } });
}

function referenceBranch(runtime) {
  const log = runtime.state.session.entries;
  const byId = new Map();
  for (const e of log) if (e && e.type !== "session") byId.set(e.id, e);
  const path = [];
  let current = byId.get(runtime.state.session.leafId || undefined);
  while (current) { path.push(current); current = current.parentId ? byId.get(current.parentId) : undefined; }
  return path.reverse();
}

// Appends, forks, leaf moves, a leaf named before its entry arrives, repeated ids, labels and session switches
// all read what an uncached walk reads, and return the log's own entry objects.
{
  const runtime = newRuntime();
  const sm = runtime.ctx.sessionManager;
  let seed = 7;
  const rand = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed >> 8 > 0 ? (seed >> 8) - Math.floor((seed >> 8) / n) * n : 0; };
  let session = "s1", total = 0, ids = [], leaf = "";
  const check = (step) => {
    const got = sm.getBranch(), want = referenceBranch(runtime);
    assert.equal(got.length, want.length, "step " + step);
    got.forEach((e, i) => assert.ok(e === want[i], "step " + step + " position " + i));
    const leafEntry = sm.getLeafEntry();
    assert.equal(leafEntry?.id, want.at(-1)?.id, "leaf entry step " + step);
    for (const e of runtime.state.session.entries) assert.ok(sm.getEntry(e.id) === runtime.state.session.entries.findLast((x) => x.id === e.id), "getEntry step " + step);
  };
  for (let step = 0; step < 600; step++) {
    const op = rand(12);
    if (op < 5 || ids.length === 0) {
      const id = session + "-e" + total;
      push(runtime, session, [entry(id, leaf || null)], total + 1, id);
      ids.push(id); leaf = id; total++;
    } else if (op === 5) {
      const batch = [];
      let parent = leaf;
      for (let k = 0, n = 1 + rand(4); k < n; k++) { const id = session + "-e" + (total + k); batch.push(entry(id, parent || null)); parent = id; }
      push(runtime, session, batch, total + batch.length, parent);
      for (const e of batch) ids.push(idOf(e));
      total += batch.length; leaf = parent;
    } else if (op < 8) {
      const parent = ids[rand(ids.length)];
      const id = session + "-e" + total;
      push(runtime, session, [entry(id, parent)], total + 1, id);
      ids.push(id); leaf = id; total++;
    } else if (op === 8) {
      leaf = ids[rand(ids.length)];
      push(runtime, session, [], total, leaf);
    } else if (op === 9) {
      // The leaf is named in one page and its entry arrives in the next.
      const id = session + "-e" + total;
      push(runtime, session, [], total, id);
      check(step + ".a");
      push(runtime, session, [entry(id, leaf || null)], total + 1, id);
      ids.push(id); leaf = id; total++;
    } else if (op === 10) {
      const target = ids[rand(ids.length)];
      const id = session + "-e" + total;
      push(runtime, session, [entry(id, leaf, { type: "label", targetId: target, label: "L" + step })], total + 1, id);
      assert.equal(sm.getLabel(target), "L" + step);
      ids.push(id); leaf = id; total++;
    } else {
      session = "s" + step;
      const id = session + "-e0";
      push(runtime, session, [entry(id, null)], 1, id);
      ids = [id]; leaf = id; total = 1;
    }
    check(step);
  }
}

// An id that occurs twice resolves to the later entry, as a rebuilt map does.
{
  const runtime = newRuntime();
  const sm = runtime.ctx.sessionManager;
  push(runtime, "s", [entry("a", null), entry("b", "a")], 2, "b");
  assert.deepEqual(sm.getBranch().map(idOf), ["a", "b"]);
  push(runtime, "s", [entry("side", "a")], 3, "b");
  assert.deepEqual(sm.getBranch().map(idOf), ["a", "b"]);
  push(runtime, "s", [entry("b", "side")], 4, "b");
  assert.deepEqual(sm.getBranch().map(idOf), ["a", "side", "b"]);
}

// A pending append is visible at once and, once the host confirms it, the branch holds the object the caller holds.
{
  const runtime = newRuntime();
  const sm = runtime.ctx.sessionManager;
  push(runtime, "s", [entry("a", null)], 1, "a");
  assert.deepEqual(sm.getBranch().map(idOf), ["a"]);
  const customId = sm.appendCustomEntry("note", { n: 1 });
  assert.deepEqual(sm.getBranch().map((e) => e.id), ["a", customId]);
  const pending = runtime.pendingEntries[0];
  push(runtime, "s", [{ ...pending }], 2, customId);
  const branch = sm.getBranch();
  assert.equal(branch.length, 2);
  assert.ok(branch[1] === pending);
  assert.ok(sm.getEntry(customId) === pending);
}

// Reads after one appended entry touch a number of entries independent of the log's length.
function costAt(n) {
  const runtime = newRuntime();
  const sm = runtime.ctx.sessionManager;
  const log = [];
  for (let i = 0; i < n; i++) log.push(entry("e" + i, i === 0 ? null : "e" + (i - 1), i === 3 ? { type: "label", targetId: "e1", label: "one" } : {}));
  push(runtime, "s", log, n, "e" + (n - 1));
  sm.getBranch(); sm.getEntry("e1"); sm.getLabel("e1");
  let best = Infinity;
  for (let round = 0; round < 5; round++) {
    const at = n + round;
    const e = entry("e" + at, "e" + (at - 1));
    reads = 0;
    push(runtime, "s", [e], at + 1, "e" + at);
    assert.equal(sm.getBranch().length, at + 1);
    assert.equal(sm.getEntry("e" + at).id, "e" + at);
    assert.equal(sm.getLabel("e1"), "one");
    assert.equal(sm.getLeafEntry().id, "e" + at);
    best = Math.min(best, reads);
  }
  return best;
}
const small = costAt(2000), large = costAt(16000);
console.log("entry id reads per append and read: n=2000 " + small + ", n=16000 " + large);
assert.ok(large <= small + 16 && large < 200, "reads grew with the log: " + small + " at n=2000, " + large + " at n=16000");
`, "__RUNTIME__", `"`+(&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()+`"`, 1)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node runtime session reads after append: %v\n%s", err, output)
	} else {
		t.Logf("%s", output)
	}
}
