// Ports test/p2/bundles.test.ts: the web bundle is named by its content: rebuilding the same source gives the same file names
// (browsers and any later shared cache keep them), no output depends on where the checkout lives, a source change gives new
// names, and a bundle over the size cap is refused.
//   P2_WEB_TREE=<scratch directory assemble.sh made> node --test build.test.mjs
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

const tree = process.env.P2_WEB_TREE ? realpathSync(process.env.P2_WEB_TREE) : undefined;
const skip = tree === undefined ? "P2_WEB_TREE (an assembled tree, see assemble.sh) is not set" : false;

/** A tree like `tree` whose apps/p2/plugins holds only the named plugin directories (each: its web.tsx text). */
function scratch(plugins) {
	const directory = mkdtempSync(join(tmpdir(), "p2-bundles-"));
	mkdirSync(join(directory, "apps/p2/plugins"), { recursive: true });
	// Copied, not linked: esbuild follows links, and the maps would then name the tree.
	for (const name of ["node_modules", "packages"]) cpSync(join(tree, name), join(directory, name), { recursive: true });
	for (const name of readdirSync(join(tree, "apps/p2")).filter((entry) => entry !== "plugins"))
		cpSync(join(tree, "apps/p2", name), join(directory, "apps/p2", name), { recursive: true });
	for (const [name, source] of Object.entries(plugins)) {
		mkdirSync(join(directory, "apps/p2/plugins", name));
		writeFileSync(join(directory, "apps/p2/plugins", name, "web.tsx"), source);
	}
	return directory;
}

function build(directory, out, plugin) {
	return spawnSync("node", [join(tree, "build.mjs"), directory, out, plugin], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
}

const files = (out) => readdirSync(out).filter((name) => name !== "bundle.json").sort();
const manifest = (out) => JSON.parse(readFileSync(join(out, "bundle.json"), "utf8"));
const probe = (word) => `export default { setup() { console.log(${JSON.stringify(word)}); } };`;

test("the web bundle's names follow its content only", { skip, timeout: 120_000 }, (t) => {
	const directory = scratch({ probe: probe("first") });
	t.after(() => rmSync(directory, { recursive: true, force: true }));
	const first = build(directory, join(directory, "out1"), "probe");
	assert.equal(first.status, 0, first.stderr);
	const again = build(directory, join(directory, "out2"), "probe");
	assert.equal(again.status, 0, again.stderr);
	assert.deepEqual(files(join(directory, "out2")), files(join(directory, "out1")));
	assert.equal(manifest(join(directory, "out2")).version, manifest(join(directory, "out1")).version);
	// Names are hashes of the files, source maps included: the checkout's absolute path must appear in none.
	for (const name of files(join(directory, "out1")))
		for (const place of [tree, directory])
			assert(!readFileSync(join(directory, "out1", name)).toString("latin1").includes(place), `${name} names ${place}`);
	writeFileSync(join(directory, "apps/p2/plugins/probe/web.tsx"), probe("second"));
	const second = build(directory, join(directory, "out3"), "probe");
	assert.equal(second.status, 0, second.stderr);
	const [before, after] = [manifest(join(directory, "out1")), manifest(join(directory, "out3"))];
	assert.notEqual(after.script, before.script);
	assert.notEqual(after.version, before.version);
	// The stylesheet comes from the unchanged runtime: its name stays, so a browser keeps it cached.
	assert.equal(after.style, before.style);
});

test("a web bundle over the size cap is refused", { skip, timeout: 120_000 }, (t) => {
	const directory = scratch({ huge: probe("x".repeat(8 * 1024 * 1024)) });
	t.after(() => rmSync(directory, { recursive: true, force: true }));
	const result = build(directory, join(directory, "out"), "huge");
	assert.notEqual(result.status, 0);
	assert.match(result.stderr, /Web bundle exceeds 8 MiB/);
});
