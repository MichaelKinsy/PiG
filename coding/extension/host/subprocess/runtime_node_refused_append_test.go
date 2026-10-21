package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/src/core/session-manager.ts appendCustomEntry and agent-session.ts:3406-3411. Pi's pi.appendEntry runs in the log's own process and always lands, so the session an extension reads is the session Pi holds.
//
// The Node runtime shows an entry it appends before the host has answered, so a read in the same handler sees it as Pi's does. A host that refuses the append (it answers the call with an error) holds no such entry, and the runtime must stop showing it: the mirror is the host's session plus the appends the host has not answered yet. It must take back only the refused entry (the others in flight stay, linked to the entries the host linked them to) and must not take back an append whose outcome it never learned (a cancelled call, a closed connection), which the host's next sync settles.
func TestNodeRuntimeStopsShowingAnAppendTheHostRefused(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(`
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { Connection, Runtime } from __RUNTIME__;

function newRuntime() {
  const runtime = new Runtime("/ext/refused.mjs");
  const socket = new EventEmitter();
  socket.write = () => true;
  runtime.conn = new Connection(socket);
  runtime.sessionLogRequested = true;
  return runtime;
}

function message(id, parentId) {
  return { type: "message", id, parentId, timestamp: "2026-10-11T00:00:00.000Z", message: { role: "user", content: "x" } };
}

// push applies one host page, as the host's state push carries it.
let hostLog = [];
function push(runtime, entries, leafId, session = "s") {
  hostLog = [...hostLog, ...entries];
  runtime.applyState({ session: { sessionId: session, leafId, entryCount: hostLog.length, entriesAppended: entries } });
}

function newSession() {
  hostLog = [];
  const runtime = newRuntime();
  push(runtime, [message("m1", null)], "m1");
  return { runtime, sm: runtime.ctx.sessionManager };
}

// answer settles the n-th unanswered appendEntry call the way the host's reply frame does.
const turn = () => new Promise((resolve) => setImmediate(resolve));
async function answer(runtime, n, callResult) {
  const calls = [...runtime.conn.pending].filter(([, call]) => call.method === "appendEntry");
  const [id] = calls[n];
  runtime.conn.resolveCall({ id, call_result: callResult });
  await turn();
}
const refuse = (runtime, n = 0) => answer(runtime, n, { error: { code: "append_failed", message: "synthetic host refusal" } });
const accept = (runtime, n = 0) => answer(runtime, n, { result: {} });
const ids = (entries) => entries.map((entry) => entry.id);

// The refused entry is no longer shown by any read, including after a later host sync, and the log is the host's.
{
  const { runtime, sm } = newSession();
  const id = sm.appendCustomEntry("fixture.denied", { text: "synthetic" }, true);
  assert.deepEqual(ids(sm.getEntries()), ["m1", id], "the append is shown before the host answers");
  assert.equal(sm.getLeafId(), id);
  await refuse(runtime);
  assert.deepEqual(ids(sm.getEntries()), ["m1"]);
  assert.equal(sm.getEntry(id), undefined);
  assert.equal(sm.getLeafId(), "m1");
  assert.equal(sm.getLeafEntry().id, "m1");
  assert.deepEqual(ids(sm.getBranch()), ["m1"]);
  assert.deepEqual(sm.getChildren("m1"), []);
  assert.deepEqual(sm.getTree().map((node) => node.entry.id), ["m1"]);
  assert.deepEqual(runtime.pendingEntries, []);
  // A host sync that reports the same log leaves nothing to reconcile.
  push(runtime, [], "m1");
  assert.deepEqual(ids(sm.getEntries()), ["m1"]);
  assert.deepEqual(ids(sm.getBranch()), ["m1"]);
}

// Only the refused entry goes. Entries in flight stay, linked to the entry the host links them to: the refused one's parent. Accepted and refused writes alternate.
{
  const { runtime, sm } = newSession();
  const refused1 = sm.appendCustomEntry("a", 1, true);
  const kept = sm.appendCustomEntry("b", 2, true);
  const refused2 = sm.appendCustomEntry("c", 3, true);
  const dependent = sm.appendCustomEntry("d", 4, true);
  assert.deepEqual(ids(sm.getBranch()), ["m1", refused1, kept, refused2, dependent]);
  const heldKept = sm.getEntry(kept);
  const heldDependent = sm.getEntry(dependent);
  await refuse(runtime, 0);
  assert.deepEqual(ids(sm.getEntries()), ["m1", kept, refused2, dependent]);
  assert.equal(sm.getEntry(kept), heldKept, "the entry in flight keeps its object");
  assert.equal(heldKept.parentId, "m1", "it takes the refused entry's parent");
  assert.deepEqual(ids(sm.getBranch()), ["m1", kept, refused2, dependent]);
  assert.equal(sm.getLeafId(), dependent);
  await refuse(runtime, 1);
  assert.deepEqual(ids(sm.getEntries()), ["m1", kept, dependent]);
  assert.equal(heldDependent.parentId, kept, "the entry after the second refusal is linked to the first entry in flight");
  assert.deepEqual(ids(sm.getBranch()), ["m1", kept, dependent]);
  assert.deepEqual(ids(sm.getChildren("m1")), [kept]);
  assert.equal(sm.getLeafId(), dependent);
  // The host takes the other two; the host's records carry the parents it linked them to.
  await accept(runtime, 0);
  await accept(runtime, 0);
  push(runtime, [{ ...heldKept }, { ...heldDependent }], dependent);
  assert.deepEqual(ids(sm.getEntries()), ["m1", kept, dependent]);
  assert.equal(sm.getEntry(kept), heldKept, "the confirmed entry is the object the caller holds");
  assert.deepEqual(runtime.pendingEntries, []);
  assert.deepEqual(ids(sm.getBranch()), ["m1", kept, dependent]);
}

// The host links an entry to its own leaf, which a native message moved after the extension's last read: the mirror takes the host's parent.
{
  const { runtime, sm } = newSession();
  const id = sm.appendCustomEntry("raced", 1, true);
  const held = sm.getEntry(id);
  assert.equal(held.parentId, "m1");
  await accept(runtime);
  push(runtime, [message("native", "m1"), { ...held, parentId: "native" }], id);
  assert.deepEqual(ids(sm.getBranch()), ["m1", "native", id]);
  assert.equal(sm.getEntry(id), held);
  assert.equal(held.parentId, "native");
  assert.equal(sm.getLeafId(), id);
}

// An entry the replicated log already holds is the host's, even when the host answers the append with an error: nothing the host has is taken back.
{
  const { runtime, sm } = newSession();
  const id = sm.appendCustomEntry("partial", 1, true);
  const held = sm.getEntry(id);
  push(runtime, [{ ...held }], id);
  await refuse(runtime);
  assert.deepEqual(ids(sm.getEntries()), ["m1", id]);
  assert.equal(sm.getEntry(id), held);
  assert.deepEqual(runtime.pendingEntries, []);
}

// An outcome the extension never learned is not a refusal: a cancelled call and a closed connection keep the entry shown until the host's log settles it.
{
  const { runtime, sm } = newSession();
  const id = sm.appendCustomEntry("unknown", 1, true);
  runtime.conn.cancelParent("");
  await turn();
  assert.deepEqual(ids(sm.getEntries()), ["m1", id], "a cancelled call");
  const held = sm.getEntry(id);
  push(runtime, [{ ...held }], id);
  assert.deepEqual(ids(sm.getEntries()), ["m1", id]);
  assert.equal(sm.getEntry(id), held);
  assert.deepEqual(runtime.pendingEntries, []);
}
{
  const { runtime, sm } = newSession();
  const id = sm.appendCustomEntry("unknown", 1, true);
  runtime.conn.onClose();
  await turn();
  assert.deepEqual(ids(sm.getEntries()), ["m1", id], "a closed connection");
}

// A record of another kind under the entry's id is not the entry: the host refuses an id its log holds, the entry stays unconfirmed until that refusal, and the host's record is never overwritten.
{
  const { runtime, sm } = newSession();
  const id = sm.appendCustomEntry("collides", 1, true);
  const hostRecord = message(id, "m1");
  push(runtime, [hostRecord], id);
  assert.equal(sm.getEntries().filter((entry) => entry.id === id).length, 2, "both are shown until the host answers");
  assert.equal(runtime.state.session.entries.at(-1), hostRecord, "the host's record is not replaced");
  await refuse(runtime);
  assert.deepEqual(sm.getEntries().map((entry) => entry.type), ["message", "message"]);
  assert.equal(sm.getEntry(id), hostRecord);
  assert.deepEqual(runtime.pendingEntries, []);
}

// A refusal that arrives after the session changed takes nothing from the new session.
{
  const { runtime, sm } = newSession();
  sm.appendCustomEntry("old", 1, true);
  hostLog = [];
  push(runtime, [message("n1", null)], "n1", "other");
  const kept = sm.appendCustomEntry("new", 2, true);
  await refuse(runtime, 0);
  assert.deepEqual(ids(sm.getEntries()), ["n1", kept]);
}

// Every append is an independent call: a refusal of the first leaves the second answerable.
{
  const { runtime, sm } = newSession();
  const first = sm.appendCustomEntry("one", 1);
  const second = sm.appendCustomEntry("two", 2);
  await accept(runtime, 1);
  await refuse(runtime, 0);
  assert.deepEqual(ids(sm.getEntries()), ["m1", second]);
  assert.equal(sm.getEntry(first), undefined);
  assert.equal(sm.getEntry(second).parentId, "m1");
}

// The host's own records that adoptHostLeafEntry shows ahead of the log (a label, a session name) are confirmed by their copies, not kept beside them.
{
  const { runtime, sm } = newSession();
  const label = { type: "label", id: "l1", parentId: "m1", timestamp: "2026-10-11T00:00:00.000Z", targetId: "m1", label: "mark" };
  runtime.pendingEntries.push(label);
  assert.deepEqual(ids(sm.getEntries()), ["m1", "l1"]);
  assert.equal(sm.getLabel("m1"), "mark");
  push(runtime, [{ ...label }], "l1");
  assert.deepEqual(ids(sm.getEntries()), ["m1", "l1"]);
  assert.equal(sm.getEntry("l1"), label);
  assert.deepEqual(runtime.pendingEntries, []);
}

// A refusal patches the cached view and id index in place. After any run of appends, answers, host pages and native messages, every read equals the one a rebuild from the same log and entries in flight gives.
{
  const { runtime, sm } = newSession();
  let seed = 11;
  const rand = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return (seed >> 8) % n; };
  const snapshot = (manager) => JSON.stringify({
    entries: manager.getEntries().map((entry) => [entry.id, entry.parentId]),
    leaf: manager.getLeafId(),
    branch: manager.getBranch().map((entry) => entry.id),
    byId: manager.getEntries().map((entry) => manager.getEntry(entry.id)?.id),
    children: manager.getChildren("m1").map((entry) => entry.id),
    tree: manager.getTree().length,
  });
  const other = newRuntime();
  const rebuilt = () => {
    other.state.session = { ...runtime.state.session, entries: [...runtime.state.session.entries] };
    other.pendingEntries = [...runtime.pendingEntries];
    other.ctx.sessionManager._view = undefined;
    other.ctx.sessionManager._cachedIndex = undefined;
    other.dropBranchCache();
    return snapshot(other.ctx.sessionManager);
  };
  const unanswered = [], accepted = [];
  let hostLeaf = "m1", natives = 0;
  for (let step = 0; step < 400; step++) {
    const op = rand(10);
    if (op < 4) {
      unanswered.push(sm.appendCustomEntry("probe", step, true));
    } else if (op < 6 && unanswered.length > 0) {
      const n = rand(unanswered.length);
      unanswered.splice(n, 1);
      await refuse(runtime, n);
    } else if (op < 7 && unanswered.length > 0) {
      const n = rand(unanswered.length);
      accepted.push(...unanswered.splice(n, 1));
      await accept(runtime, n);
    } else if (op < 9 && accepted.length > 0) {
      const id = accepted.splice(rand(accepted.length), 1)[0];
      const held = runtime.pendingEntries.find((entry) => entry.id === id);
      assert.ok(held, "an accepted entry is held until the host's log has it");
      push(runtime, [{ ...held, parentId: hostLeaf }], id);
      hostLeaf = id;
    } else if (op === 9) {
      const id = "native-" + natives++;
      push(runtime, [message(id, hostLeaf)], id);
      hostLeaf = id;
    }
    if (rand(3) === 0) assert.equal(snapshot(sm), rebuilt(), "step " + step);
  }
  assert.equal(snapshot(sm), rebuilt(), "the end");
}
`, "__RUNTIME__", `"`+(&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()+`"`, 1)
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node runtime after a refused append: %v\n%s", err, output)
	}
}

// refusingHost is a host whose session log an extension replicates and whose appendEntry action takes an append like the one every mode binds (the id and timestamp the extension chose, the host's own leaf as the parent) unless it refuses the type.
type refusingHost struct {
	mu      sync.Mutex
	entries []json.RawMessage
	records []map[string]any
	leaf    string
	refuse  func(customType string) bool
	// nativeBefore names a custom type the host appends a native message ahead of, as a message it appended while the extension's call was in flight.
	nativeBefore string
	calls        []string
}

func (h *refusingHost) native(id string) {
	h.add(map[string]any{"type": "message", "id": id, "parentId": h.parent(), "timestamp": "2026-10-11T00:00:00.000Z", "message": map[string]any{"role": "user", "content": "x"}})
}

func (h *refusingHost) parent() any {
	if h.leaf == "" {
		return nil
	}
	return h.leaf
}

func (h *refusingHost) add(record map[string]any) {
	raw, err := json.Marshal(record)
	if err != nil {
		panic(err)
	}
	h.entries = append(h.entries, raw)
	h.records = append(h.records, record)
	h.leaf = record["id"].(string)
}

func (h *refusingHost) callbacks() *HostCallbacks {
	return &HostCallbacks{
		GetSessionID: func() string { return "refused-session" },
		GetLeafID: func() string {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.leaf
		},
		GetEntriesPage: func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if cursor < 0 || cursor > len(h.entries) {
				cursor = 0
			}
			return append([]json.RawMessage{}, h.entries[cursor:]...), len(h.entries), false, h.leaf
		},
		AppendEntry: func(customType string, data any, direct *DirectEntryAppend) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.calls = append(h.calls, customType)
			if h.refuse(customType) {
				return errors.New("synthetic host refusal")
			}
			if customType == h.nativeBefore {
				h.native("native-1")
			}
			h.add(map[string]any{"type": "custom", "customType": customType, "data": data, "id": direct.ID, "parentId": h.parent(), "timestamp": direct.Timestamp})
			return nil
		},
	}
}

// sessionView is what the extension's read tool reports about the session, and what the host reports in the same shape: every entry's id, type, custom type and parent, the branch from the leaf, and the leaf. The members are in the order the tool writes them.
type sessionView struct {
	Entries [][4]any `json:"entries"`
	Branch  []string `json:"branch"`
	Leaf    string   `json:"leaf"`
}

func (h *refusingHost) view() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	byID := map[string]map[string]any{}
	view := sessionView{Entries: [][4]any{}, Branch: []string{}, Leaf: h.leaf}
	for _, record := range h.records {
		byID[record["id"].(string)] = record
		view.Entries = append(view.Entries, [4]any{record["id"], record["type"], record["customType"], record["parentId"]})
	}
	for current := h.leaf; current != ""; {
		view.Branch = append([]string{current}, view.Branch...)
		current, _ = byID[current]["parentId"].(string)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

const refusedAppendFixture = `export default function (pi) {
  pi.registerTool({ name: "append_probe", label: "Append", description: "Append one record per type", parameters: { type: "object", properties: { types: { type: "array", items: { type: "string" } } } },
    async execute(_id, args) { for (const type of args.types) pi.appendEntry(type, { text: type }); return { content: [{ type: "text", text: "requested" }], details: {} }; } });
  pi.registerTool({ name: "read_probe", label: "Read", description: "Read the mirror", parameters: { type: "object" },
    async execute(_id, _args, _signal, _update, ctx) {
      const manager = ctx.sessionManager;
      const entries = manager.getEntries().map((entry) => [entry.id, entry.type, entry.customType ?? null, entry.parentId]);
      const text = JSON.stringify({ entries, branch: manager.getBranch().map((entry) => entry.id), leaf: manager.getLeafId() ?? "" });
      return { content: [{ type: "text", text }], details: {} };
    } });
}`

// The program of the issue (a host that refuses pi.appendEntry), through the real Host and Node runtime, with the host's other appends around it. Each read is a host-synced tool call after the append; every one must report exactly the host's session.
func TestNodeExtensionReadsExactlyTheSessionTheHostHolds(t *testing.T) {
	cases := []struct {
		name         string
		types        []string
		refuse       func(string) bool
		nativeBefore string
		want         []string // the custom types the host holds afterwards
	}{
		{
			name:   "one refused append",
			types:  []string{"fixture.denied"},
			refuse: func(string) bool { return true },
		},
		{
			name:   "accepted append between refused ones",
			types:  []string{"fixture.denied", "fixture.kept", "fixture.denied"},
			refuse: func(customType string) bool { return customType == "fixture.denied" },
			want:   []string{"fixture.kept"},
		},
		{
			name:         "native message the host appended while the append was in flight",
			types:        []string{"fixture.kept", "fixture.denied", "fixture.raced"},
			refuse:       func(customType string) bool { return customType == "fixture.denied" },
			nativeBefore: "fixture.raced",
			want:         []string{"fixture.kept", "fixture.raced"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			root := t.TempDir()
			source := filepath.Join(root, "fixture.mjs")
			if err := os.WriteFile(source, []byte(refusedAppendFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			host := &refusingHost{refuse: tc.refuse, nativeBefore: tc.nativeBefore}
			host.native("m1")
			bridge := NewUIBridge(func() {})
			bridge.SetActions(host.callbacks())
			h := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			h.SetUIBridge(bridge)
			defer h.Shutdown("test complete")
			loaded, err := h.Load(ctx, ExtConfig{Name: "fixture", Source: source, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			params, err := json.Marshal(map[string]any{"types": tc.types})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loaded.Tools["append_probe"].Definition.Execute(ctx, "append", params, nil); err != nil {
				t.Fatal(err)
			}
			host.mu.Lock()
			calls := append([]string{}, host.calls...)
			var held []string
			for _, record := range host.records {
				if customType, ok := record["customType"].(string); ok {
					held = append(held, customType)
				}
			}
			host.mu.Unlock()
			if strings.Join(calls, ",") != strings.Join(tc.types, ",") {
				t.Fatalf("the host received appends %v, want %v", calls, tc.types)
			}
			if strings.Join(held, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("the host holds custom entries %v, want %v", held, tc.want)
			}
			for round := range 2 {
				result, err := loaded.Tools["read_probe"].Definition.Execute(ctx, "read", json.RawMessage(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := ai.ContentText(result.Content), host.view(); got != want {
					t.Fatalf("read %d: the extension's mirror differs from the host's session\n mirror %s\n host   %s", round, got, want)
				}
			}
		})
	}
}
