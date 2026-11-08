package plugins

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Node host's store (StoreOptions; docs/plan/chord-plugins-node-evaluator.md section 10.5): Pi's `createHost({ store })`,
// opened in the subprocess as the apps of Pi's examples open it (next/examples 20-tables.ts and 21-persisted-documents.ts:
// `openStore(sqliteBackend(await openNodeSqliteDatabase(path)))`). The facet below is those two examples' History and Notebook
// in one local type: a persisted singleton document, a persisted keyed document and a table, written in one `self.tx`.

// storeNotebookPlugin is the plugin whose facet declares the documents and the table.
const storeNotebookPlugin = `import { arr, defineFacet, defineState, defineTable, int, local, method, obj, query, str } from "@earendil-works/chord";
const Tags = defineState("tags", { schema: obj({ tags: arr(str()) }), version: 1, initial: () => ({ tags: [] }), persist: true, checkpointWhen: () => false });
const Notes = defineState("notes", { schema: obj({ body: str() }), version: 1, initial: () => ({ body: "" }), keyed: true, persist: true, checkpointWhen: () => false });
const Edits = defineTable("edits", { version: 1, columns: { id: str(), at: int() }, key: ["id"] });
const Notebook = local("test.notebook", { version: 1, params: {}, members: {
	tags: obj({ tags: arr(str()) }),
	write: method(obj({ id: str(), body: str(), tag: str() })),
	read: method(str(), str()),
	rows: method(obj({}), int()),
} });
export default defineFacet((host) => host.serve(Notebook, (_params, self) => {
	const tags = self.state(Tags);
	const notes = self.state(Notes);
	const edits = self.table(Edits);
	return {
		tags,
		write: async ({ id, body, tag }, ctx) => {
			await self.tx(async (tx) => {
				(await tx.draft(notes, id)).body = body;
				const draft = await tx.draft(tags);
				if (!draft.tags.includes(tag)) draft.tags.push(tag);
				tx.put(edits, { id, at: 1 });
			}, ctx);
			return null;
		},
		read: async (id, ctx) => (await notes.get(id, ctx))?.body ?? "(none)",
		rows: async (_args, ctx) => edits.count(query(Edits), ctx),
	};
}));`

// storeNotebookOps are the probe's ops over that local type, held in the subprocess.
const storeNotebookOps = `
write: async (args, { ctx }) => {
	const notebook = await ctx.get(next.local("test.notebook", { version: 1, params: {}, members: { tags: next.obj({ tags: next.arr(next.str()) }), write: next.method(next.obj({ id: next.str(), body: next.str(), tag: next.str() })), read: next.method(next.str(), next.str()), rows: next.method(next.obj({}), next.int()) } }), {});
	await notebook.write(args, ctx);
	return null;
},
read: async ({ id }, { ctx }) => {
	const notebook = await ctx.get(next.local("test.notebook", { version: 1, params: {}, members: { tags: next.obj({ tags: next.arr(next.str()) }), write: next.method(next.obj({ id: next.str(), body: next.str(), tag: next.str() })), read: next.method(next.str(), next.str()), rows: next.method(next.obj({}), next.int()) } }), {});
	return { body: await notebook.read(id, ctx), tags: notebook.tags.value.tags, rows: await notebook.rows({}, ctx) };
},
lock: async ({ path }) => {
	const { DatabaseSync } = await import("node:sqlite");
	globalThis.locked = new DatabaseSync(path);
	globalThis.locked.exec("BEGIN EXCLUSIVE");
	return null;
},
`

// storeLoad is a Node host of the notebook plugin on the store at path, closed when the test ends.
func storeLoad(t *testing.T, c *pcComponent, path string) pcLoaded {
	t.Helper()
	return c.loadWorkerWith(t, storeNotebookOps, nil, func(load *PluginsLoad) { load.Store = &StoreOptions{Path: path} })
}

func storeNotebook(t *testing.T) (*pcComponent, string, chan NodeHostError) {
	t.Helper()
	root := tempRoot(t)
	pcPluginSource(t, root, "notebook", storeNotebookPlugin)
	errors := make(chan NodeHostError, 16)
	c := pcNew(t, root, pcOptions{hostError: func(err NodeHostError) { errors <- err }})
	return c, filepath.Join(t.TempDir(), "notebooks", "store.db"), errors
}

// The documents and the table of a plugin's type are written to the store, and a Node host loaded again on the same file
// finds them (21-persisted-documents.ts: "a new host on the same file: the documents come back from the store").
func TestStoreAPluginsPersistedDocumentsAndTableSurviveClosingTheNodeHost(t *testing.T) {
	c, path, _ := storeNotebook(t)
	first := storeLoad(t, c, path)
	first.probe.must("write", map[string]any{"id": "groceries", "body": "oat milk", "tag": "home"})
	first.probe.must("write", map[string]any{"id": "standup", "body": "demo the store", "tag": "work"})
	got := first.probe.must("read", map[string]any{"id": "standup"}).(map[string]any)
	if got["body"] != "demo the store" || got["rows"] != float64(2) {
		t.Fatalf("first host reads %v", got)
	}
	if err := first.node.Close(background); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := storeLoad(t, c, path)
	got = second.probe.must("read", map[string]any{"id": "groceries"}).(map[string]any)
	if got["body"] != "oat milk" || got["rows"] != float64(2) {
		t.Fatalf("second host reads %v, want the first host's note and its two rows", got)
	}
	if tags, _ := got["tags"].([]any); len(tags) != 2 || tags[0] != "home" || tags[1] != "work" {
		t.Fatalf("second host's tags = %v, want [home work]", got["tags"])
	}
}

// A host loaded without a store is Pi's host created without one: the type fails to activate with Pi's own error.
func TestStoreAFacetWithAPersistedDocumentFailsToActivateWhenTheHostIsLoadedWithoutAStore(t *testing.T) {
	root := tempRoot(t)
	pcPluginSource(t, root, "notebook", storeNotebookPlugin)
	failures := make(chan NodeHostError, 8)
	c := pcNew(t, root, pcOptions{hostError: func(err NodeHostError) { failures <- err }})
	h := c.loadWorker(t, storeNotebookOps, nil)
	go func() { _, _ = h.probe.call("read", map[string]any{"id": "x"}) }()
	for {
		select {
		case err := <-failures:
			if err.Kind == "activation_failed" && err.Object == "test.notebook" {
				if err.Failure.Error == nil || !strings.Contains(err.Failure.Error.Error(), "persist needs a store") {
					t.Fatalf("activation failed with %+v, want persist needs a store", err.Failure)
				}
				return
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the host reported no failed activation")
		}
	}
}

// A store without a path is refused by the load, before a process starts.
func TestStoreWithoutAPathIsRefused(t *testing.T) {
	c, _, _ := storeNotebook(t)
	_, _, err := c.Load(PluginsLoad{Kind: "worker", Peer: serverPrincipal, Store: &StoreOptions{}}, background)
	if err == nil || !strings.Contains(err.Error(), "Store.Path is required") {
		t.Fatalf("load without a store path: %v", err)
	}
}

// A store that stops after a backend error (Pi: `unavailable/storage_failed`) is reported once to NodeOptions.OnError and
// closes the host (host.test.ts "reports storage_failed once and closes the host"). Here another connection holds the file's
// write lock, so the store's `BEGIN` fails once SQLite's busy timeout (5 s) ends.
func TestStoreAFailedStoreReachesOnErrorOnceAndClosesTheHost(t *testing.T) {
	c, path, errors := storeNotebook(t)
	h := storeLoad(t, c, path)
	h.probe.must("write", map[string]any{"id": "a", "body": "one", "tag": "x"})
	h.probe.must("lock", map[string]any{"path": path})
	if _, err := h.probe.call("write", map[string]any{"id": "b", "body": "two", "tag": "y"}); err == nil {
		t.Fatal("a write under a lock held by another connection succeeded")
	}
	var failed NodeHostError
	deadline := time.After(30 * time.Second)
	for failed.Kind != "storage_failed" {
		select {
		case failed = <-errors:
		case <-deadline:
			t.Fatal("the host reported no storage_failed")
		}
	}
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), "locked") {
		t.Fatalf("storage_failed's error = %v, want SQLite's database is locked", failed.Err)
	}
	select {
	case again := <-errors:
		if again.Kind == "storage_failed" {
			t.Fatalf("storage_failed reported twice: %+v", again)
		}
	case <-time.After(500 * time.Millisecond):
	}
}

// Two Node hosts on one file are two stores, and Pi's rule is one open host per store (host.ts: "One open host per store"): it
// is checked in memory, so nothing refuses the second open. Pi's own hosts behave the same in one process (the oracle ran
// two `openStore` + `createHost` pairs on one `openNodeSqliteDatabase` file): both load, the second reads what the first
// committed, and the second's write, whose id allocator is stale, is refused by the file's constraint as `unavailable`
// "the store refused the commit (UNIQUE constraint failed: chord_documents.id)", reported as `object_failed`; the first
// host goes on.
func TestStoreTwoNodeHostsOnOneFileAreTwoStoresAsInPi(t *testing.T) {
	c, path, errors := storeNotebook(t)
	first := storeLoad(t, c, path)
	second := storeLoad(t, c, path)
	first.probe.must("write", map[string]any{"id": "x", "body": "from first", "tag": "a"})
	if got := second.probe.must("read", map[string]any{"id": "x"}).(map[string]any)["body"]; got != "from first" {
		t.Fatalf("the second host reads %v, want what the first committed", got)
	}
	_, err := second.probe.call("write", map[string]any{"id": "y", "body": "from second", "tag": "b"})
	const want = "the store refused the commit (UNIQUE constraint failed: chord_documents.id)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("the second host's write = %v, want %q", err, want)
	}
	for failed := false; !failed; {
		select {
		case reported := <-errors:
			failed = reported.Kind == "object_failed"
		case <-time.After(30 * time.Second):
			t.Fatal("the second host reported no object_failed")
		}
	}
	if got := first.probe.must("read", map[string]any{"id": "y"}).(map[string]any)["body"]; got != "(none)" {
		t.Fatalf("the first host reads %v for the refused write, want (none)", got)
	}
	first.probe.must("write", map[string]any{"id": "z", "body": "again", "tag": "c"})
}
