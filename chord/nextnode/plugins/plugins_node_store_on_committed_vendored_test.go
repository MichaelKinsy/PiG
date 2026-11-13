package plugins

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Ports the three cases of `describe("onCommitted")` of packages/chord/test/store/store.test.ts (Pi 6268cc5cd,
// `ChordStore.onCommitted`; lines 370-440 at Pi 90ed3e3b14b5) over each vendored tree: the vendored trees carry that
// commit's store/store.ts, and a plugin of the Node host that opens a store of its own (the tree's API root exports
// `openStore` and `sqliteBackend`) is the caller that meets it. The Go twin of the same cases over the Go store is
// chord/next/store/store_test.go, TestStore/onCommitted.
//
// Each case has Pi's inputs and Pi's expectations (`expect` is `node:assert/strict` here: the vendored trees hold no
// vitest), on a store over a fresh `:memory:` database, and prints its name once every expectation held. The client
// tree holds no SQLite driver (a page has none): its store runs on a database opened by the Node host's tree's
// sqlite/node.ts, which is only the `node:sqlite` adapter; the store, its engine and its backend are the client
// tree's own files.
const ocCases = `
const ADDRESS = { namespace: "chord", owner: "app.store", kind: "notes", key: "" };
const BASE = { version: 1, fingerprint: "0000000000000000", value: { n: 0 } };
const BROKEN = {
	order: "write",
	apply: (step) =>
		step.run("INSERT INTO no_such_table VALUES (1)").then(() => [{ kind: "document.retire", id: 0, revision: 0 }]),
};
const memoryStore = async () => openStore(sqliteBackend(await openNodeSqliteDatabase(":memory:")), context);
const recording = (adopted = []) => ({
	adopted,
	fence: { admit: () => true, adopt: (seq, ids) => void adopted.push([seq, ids]) },
});
const cases = {
	"announces the new lastSeq after commits are durable, once per microtask, never inside a commit": async () => {
		const store = await memoryStore();
		const seen = [];
		let inside = false;
		const remove = store.onCommitted(
			(seq) => {
				assert.equal(inside, false);
				seen.push(seq);
			},
			(error) => {
				throw error;
			},
		);
		const { fence } = recording();
		inside = true;
		const first = store.commit([createDocument(ADDRESS, BASE, "{}")], fence, context);
		inside = false;
		await first;
		await Promise.resolve();
		assert.deepEqual(seen, [store.lastSeq]);
		// Several commits before the announcement runs: one call, with the last seq.
		await store.commit([createDocument({ ...ADDRESS, key: "b" }, BASE, "{}")], fence, context);
		await store.commit([createDocument({ ...ADDRESS, key: "c" }, BASE, "{}")], fence, context);
		await new Promise((resolve) => setTimeout(resolve, 0));
		assert.equal(seen.at(-1), store.lastSeq);
		remove();
		await store.commit([createDocument({ ...ADDRESS, key: "d" }, BASE, "{}")], fence, context);
		await new Promise((resolve) => setTimeout(resolve, 0));
		assert.equal(seen.at(-1), store.lastSeq - 1);
	},
	"announces a series and a refused commit not at all": async () => {
		const store = await memoryStore();
		const seen = [];
		store.onCommitted(
			(seq) => void seen.push(seq),
			(error) => {
				throw error;
			},
		);
		await store.commitSeries(
			[{ check: async () => {}, writes: [createDocument(ADDRESS, BASE, "{}")] }],
			() => {},
			context,
		);
		await new Promise((resolve) => setTimeout(resolve, 0));
		assert.deepEqual(seen, [store.lastSeq]);
		const before = seen.length;
		await assert.rejects(store.commit([BROKEN], recording().fence, context));
		await new Promise((resolve) => setTimeout(resolve, 0));
		assert.equal(seen.length, before);
	},
	"removes a listener that throws and hands its error to onError; the commit stands": async () => {
		const store = await memoryStore();
		const errors = [];
		let calls = 0;
		store.onCommitted(
			() => {
				calls++;
				throw new Error("listener");
			},
			(error) => void errors.push(error),
		);
		const { fence } = recording();
		await store.commit([createDocument(ADDRESS, BASE, "{}")], fence, context);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await store.commit([createDocument({ ...ADDRESS, key: "b" }, BASE, "{}")], fence, context);
		await new Promise((resolve) => setTimeout(resolve, 0));
		assert.equal(calls, 1);
		assert.equal(errors.length, 1);
		assert.equal(store.lastSeq, 2);
	},
};
await cases[process.env.OC_CASE]();
console.log("held: " + process.env.OC_CASE);
`

// ocTitles are Pi's three case titles, in Pi's order.
var ocTitles = []string{
	"announces the new lastSeq after commits are durable, once per microtask, never inside a commit",
	"announces a series and a refused commit not at all",
	"removes a listener that throws and hands its error to onError; the commit stands",
}

// vendoredNode runs script on Node over tree (the layout of the extracted Node tree, as shStart runs a consumer) and
// returns what it printed and whether Node ended without an error. A machine without node skips, as the package's
// other tests over a vendored tree do.
func vendoredNode(t *testing.T, tree string, env []string, script func(pi func(string) string) string) (string, error) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on the PATH")
	}
	pi := func(path string) string {
		return pcJSON(t, fileURL(filepath.Join(tree, "pi", "chord", "src", filepath.FromSlash(path))))
	}
	body := strings.Join([]string{
		`import { registerHooks } from "node:module";`,
		"registerHooks({ resolve(specifier, context, next) {",
		fmt.Sprintf("  if (specifier === \"@earendil-works/pi-schema\") return { url: %s, shortCircuit: true };",
			pcJSON(t, fileURL(filepath.Join(tree, "pi", "schema", "src", "index.ts")))),
		"  return next(specifier, context);",
		"} });",
		script(pi),
	}, "\n")
	command := exec.Command(node, "--experimental-strip-types", "--no-warnings", "--input-type=module", "--eval", body)
	command.Env = append(command.Environ(), env...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func TestTheVendoredTreesStoreAnnouncesItsCommits(t *testing.T) {
	nodeTree, err := extractNode(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The `node:sqlite` adapter of the Node host's tree, for both trees (the client tree has none).
	driver := pcJSON(t, fileURL(filepath.Join(nodeTree, "pi", "chord", "src", "sqlite", "node.ts")))
	for _, each := range shTrees {
		t.Run(each.name, func(t *testing.T) {
			tree := each.tree(t)
			for _, title := range ocTitles {
				t.Run(title, func(t *testing.T) {
					output, err := vendoredNode(t, tree, []string{"OC_CASE=" + title}, func(pi func(string) string) string {
						return strings.Join([]string{
							`import assert from "node:assert/strict";`,
							"const { BACKGROUND_CONTEXT: context } = await import(" + pi("context/index.ts") + ");",
							"const { openNodeSqliteDatabase } = await import(" + driver + ");",
							"const { sqliteBackend } = await import(" + pi("store/backend.ts") + ");",
							"const { createDocument } = await import(" + pi("store/engine.ts") + ");",
							"const { openStore } = await import(" + pi("store/store.ts") + ");",
							ocCases,
						}, "\n")
					})
					if err != nil || output != "held: "+title {
						t.Fatalf("node: %v\n%s", err, output)
					}
				})
			}
		})
	}
}

// Not upstream's: the client tree's telemetry-ingest/facet.ts is Pi's file with `admit` (Pi 7ae9ff8c7): the facet as
// the tree loads it asks `options.admit(principal)` before it serves a page and refuses with the forbidden error the
// Go facet gives. Nothing of PiG serves that file (the server is Go: TestTheClientTreesPageSenderIsRefusedByAGoIngest
// FacetThatDoesNotAdmitItsPrincipal runs the tree's sender against the Go facet); this is the tree's file being the
// one the Go port follows.
func TestTheClientTreesIngestFacetAsksAdmit(t *testing.T) {
	output, err := vendoredNode(t, shClientTree(t), nil, func(pi func(string) string) string {
		return `const ingest = await import(` + pi("telemetry-ingest/index.ts") + `);
const source = ingest.telemetryIngestFacet.toString();
console.log(JSON.stringify({
	facet: typeof ingest.telemetryIngestFacet,
	asksAdmit: source.includes("options.admit(principal)"),
	refuses: source.includes("chord.telemetry.page: not admitted"),
}));`
	})
	if want := `{"facet":"function","asksAdmit":true,"refuses":true}`; err != nil || output != want {
		t.Fatalf("the client tree's ingest facet: %v\n%s\nwant %s", err, output, want)
	}
}
