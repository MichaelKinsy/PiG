import assert from "node:assert/strict"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { Capture } from "../lib/capsql.mjs"
import { sample } from "../lib/matrix.mjs"
import { classify, diffCommits, diffContexts, diffOutput } from "../lib/rowdiff.mjs"
import { parseToml } from "../lib/toml.mjs"

test("toml: the subset corpus.toml uses, and nothing else", () => {
	const t = parseToml('# c\n[pin]\nx = "a#b" # c\n[[s]]\nid = "E00"\nl = ["a", "b"]\nn = [\n "x",\n "y",\n]\nt = { a = "b", c = 3 }\n[[s]]\nid = "E01"\nb = true\nk = 12\n')
	assert.deepEqual(t, { pin: { x: "a#b" }, s: [{ id: "E00", l: ["a", "b"], n: ["x", "y"], t: { a: "b", c: 3 } }, { id: "E01", b: true, k: 12 }] })
	assert.throws(() => parseToml("x = 1.2.3"), /trailing|cannot/)
	assert.throws(() => parseToml("x = @"), /cannot parse value/)
})

test("classify: key order, number format and value are different failures of the same text", () => {
	assert.equal(classify('{"a":1}', '{"a":1}'), undefined)
	assert.equal(classify('{"a":1,"b":2}', '{"b":2,"a":1}').kind, "key-order")
	assert.equal(classify('{"a":1}', '{"a":1.0}').kind, "number-format-or-escape")
	assert.equal(classify('{"a":"\\u00e9"}', '{"a":"é"}').kind, "number-format-or-escape")
	const v = classify('{"a":{"b":[1,2]}}', '{"a":{"b":[1,3]}}')
	assert.deepEqual([v.kind, v.path], ["value", "$.a.b[1]"])
	assert.equal(classify("x", "y").kind, "text")
})

const commit = (store, seq, changes) => ({ t: "commit", store, seq, changes })
const row = (table, after) => ({ table, op: "insert", pk: [1], after })

test("diffCommits names the first divergent commit, its table and the differing column", () => {
	const a = [commit("s0", 1, [row("entries", [1, "x"])]), commit("s0", 2, [row("entries", [2, '{"a":1,"b":2}'])])]
	const b = [commit("s0", 1, [row("entries", [1, "x"])]), commit("s0", 2, [row("entries", [2, '{"b":2,"a":1}'])])]
	const d = diffCommits(a, b)
	assert.equal(d.seq, 2); assert.equal(d.table, "entries"); assert.equal(d.columns[0].kind, "key-order")
	assert.equal(diffCommits(a, a), undefined)
	assert.equal(diffCommits(a, a.slice(0, 1)).problem, "only in A")
	assert.equal(diffCommits(a, [a[0], commit("s0", 3, a[1].changes)]).problem, "different seq")
})

test("diffContexts compares the fingerprints it is told to and ignores the others", () => {
	const a = [{ t: "model", ctx: "1", bench: "b" }], b = [{ t: "model", bench: "b" }]
	assert.ok(diffContexts(a, b))
	assert.equal(diffContexts(a, b, ["bench"]), undefined)
	assert.ok(diffContexts(a, [{ t: "model", ctx: "1", bench: "c" }]))
})

test("diffOutput reports a differing line and a differing exit status", () => {
	const o = (...t) => t.map(text => ({ t: "out", text }))
	assert.equal(diffOutput(o("a", "b"), o("a", "b")), undefined)
	assert.deepEqual(diffOutput(o("a", "b"), o("a", "c")), { level: "output", line: 1, a: "b", b: "c" })
	assert.equal(diffOutput([...o("a"), { t: "end", code: 0 }], [...o("a"), { t: "end", code: 1 }]).problem, "exit status")
})

test("sample: first and last crash point are always included", () => {
	const points = Array.from({ length: 77 }, (_, i) => 500 + i)
	const s = sample(points, 10)
	assert.equal(s.length, 10); assert.equal(s[0], 500); assert.equal(s.at(-1), 576)
	assert.deepEqual(sample(points, 0), points)
})

test("capsql: a commit is the same whatever order its statements touched the tables in", () => {
	const dir = mkdtempSync(join(tmpdir(), "capsql-test-"))
	try {
		const lines = []
		const make = (name, order) => {
			const cap = new Capture(join(dir, `${name}.sqlite`), { label: name, sink: l => lines.push(l) })
			cap.exec("CREATE TABLE durable_metadata (singleton INTEGER PRIMARY KEY, next_id TEXT NOT NULL, next_seq INTEGER NOT NULL); INSERT INTO durable_metadata VALUES (1, '2', 5); CREATE TABLE a (id INTEGER PRIMARY KEY, v TEXT); CREATE TABLE b (id INTEGER PRIMARY KEY, v TEXT)")
			cap.begin()
			for (const t of order) cap.run(`INSERT INTO ${t} (id, v) VALUES (?, ?)`, [1, t])
			cap.run("UPDATE durable_metadata SET next_seq = ? WHERE singleton = 1", [6])
			cap.commit()
			cap.finish()
		}
		make("x", ["a", "b"]); make("y", ["b", "a"])
		const [x, y] = ["x", "y"].map(s => lines.find(l => l.t === "commit" && l.store === s))
		assert.equal(x.seq, 5)
		assert.deepEqual(x.changes, y.changes)
		assert.deepEqual(x.changes.map(c => c.table), ["a", "b", "durable_metadata"])
		assert.deepEqual(x.stmts.map(s => s.k), ["w", "w", "w"])
	} finally { rmSync(dir, { recursive: true, force: true }) }
})

test("capsql: tables the sidecar and workerd own never reach a commit line", () => {
	const dir = mkdtempSync(join(tmpdir(), "capsql-test-"))
	try {
		const lines = []
		const cap = new Capture(join(dir, "s.sqlite"), { label: "s", sink: l => lines.push(l) })
		cap.exec("CREATE TABLE pig_index (id INTEGER PRIMARY KEY, v TEXT); CREATE TABLE _cf_KV (id INTEGER PRIMARY KEY, v TEXT)")
		cap.begin(); cap.run("INSERT INTO pig_index VALUES (1, 'x')"); cap.run("INSERT INTO _cf_KV VALUES (1, 'x')"); cap.commit()
		assert.equal(lines.filter(l => l.t === "commit").length, 0)
		cap.finish()
	} finally { rmSync(dir, { recursive: true, force: true }) }
})
