// The durable-bench workload (github.com/clavia-labs/durable-bench) on pi-durable 1.0.4 over node:sqlite: the Node twin of
// bench/durableperf/main.go. It runs from durable/interop so the pinned packages resolve (`make durable-interop-deps`).
//
//   node --no-warnings durable/interop/bench.mjs seed   DIR [sizes]
//   node --no-warnings durable/interop/bench.mjs sample FIXTURE TURNS [index]
//   OUT=results.jsonl node --no-warnings durable/interop/bench.mjs run DIR [sizes] [samples]
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { appendFileSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { DatabaseSync } from "node:sqlite";
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context";
import { Type } from "@earendil-works/pi-ai";
import { createModels } from "@earendil-works/pi-ai/models";
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux";
import { createRegistry, defineExtension, defineTool, Harness, section } from "@earendil-works/pi-durable";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";

// The pi-durable version that resolved here, for the result lines: durable/interop's lockfile, or another locked install
// the script is copied next to (bench/k8s/image/pi-durable).
const piDurableVersion = JSON.parse(readFileSync(new URL("../package.json", import.meta.resolve("@earendil-works/pi-durable")), "utf8")).version;

// COUNT=1 prints, per measured turn, the SQL statements, rows and JSON characters the storage reads and writes.
const counters = { statements: 0, reads: 0, writes: 0, rows: 0, chars: 0 };
if (process.env.COUNT) {
	const proto = Object.getPrototypeOf(new DatabaseSync(":memory:").prepare("SELECT 1"));
	const weigh = (rows) => { for (const row of rows) { counters.rows++; for (const v of Object.values(row)) if (typeof v === "string") counters.chars += v.length; } };
	for (const method of ["all", "get", "run"]) {
		const original = proto[method];
		proto[method] = function (...args) {
			counters.statements++;
			const result = original.apply(this, args);
			if (method === "run") counters.writes++;
			else { counters.reads++; weigh(method === "all" ? result : result ? [result] : []); }
			return result;
		};
	}
}

const CYCLE = [1, 1, 0];
const MEASURED_TURNS = 10;
const MEASURED_TOOLS = 8;
const BATCH = 50;
const SIZES = [50, 250, 1000, 3500];
const SYSTEM = "You are a benchmark agent. Call lookup as instructed, then answer briefly.";

const turn = (id, tools) => ({ id, text: `turn ${id} tools=${tools}` });
const history = (from, to) => Array.from({ length: to - from }, (_, i) => turn(`h${from + i}`, CYCLE[(from + i) % CYCLE.length]));
const payload = (n) => {
	const head = `record ${n}: `;
	return head + "x".repeat((n % 97 === 0 ? 8192 : 256) - head.length);
};
function next(context) {
	const last = context.findLastIndex((m) => m.role === "user");
	const planned = Number(/tools=(\d+)/.exec(context[last]?.text ?? "")?.[1] ?? 0);
	const total = context.filter((m) => m.role === "tool").length;
	const done = context.slice(last + 1).filter((m) => m.role === "tool").length;
	return done < planned ? { call: total + 1 } : { answer: `done after ${done} lookups` };
}
const fingerprint = (context) =>
	createHash("sha256").update(JSON.stringify(context.map((m) => [m.role, m.text, m.calls ?? []]))).digest("hex").slice(0, 16);

async function open(path) {
	const faux = fauxProvider({ models: [{ id: "scripted-1", contextWindow: 1e9 }], tokenSize: { min: 1e9, max: 1e9 } });
	const models = createModels();
	models.setProvider(faux.provider);
	const state = { seen: [] };
	const respond = (transcript) => {
		faux.appendResponses([respond]);
		state.seen = transcript.messages
			.filter((m) => ["user", "assistant", "toolResult"].includes(m.role))
			.map((m) => {
				const blocks = typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content;
				const text = blocks.filter((b) => b.type === "text").map((b) => b.text).join("");
				const calls = blocks.filter((b) => b.type === "toolCall").map((b) => b.arguments.n);
				return { role: m.role === "toolResult" ? "tool" : m.role, text, ...(calls.length ? { calls } : {}) };
			});
		const step = next(state.seen);
		return "call" in step
			? fauxAssistantMessage(fauxToolCall("lookup", { n: step.call }, { id: `call-${step.call}` }), { stopReason: "toolUse" })
			: fauxAssistantMessage(step.answer);
	};
	faux.setResponses([respond]);
	const lookup = defineTool({
		name: "lookup",
		description: "Look up record number n",
		parameters: Type.Object({ n: Type.Number() }),
		execute: async ({ n }) => ({ content: [{ type: "text", text: payload(n) }] }),
	});
	const registry = createRegistry();
	registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => SYSTEM, { tag: false })] }));
	const harness = await Harness.open(await openNodeSqliteStorage(path), { models, registry, settings: { compaction: { enabled: false } } }, context);
	const model = faux.getModel();
	const conversation = await harness.root(context, { agent: { model: { provider: model.provider, modelId: model.id } } });
	return { harness, conversation, state };
}

async function runTurn(app, { id, text }) {
	const submission = await app.conversation.submit({ type: "input", content: text, requestId: id }, context);
	const settled = await submission.wait(context);
	if (settled.status !== "done") throw new Error(JSON.stringify(settled));
	await app.conversation.waitForIdle(context);
}

function tables(path) {
	const db = new DatabaseSync(path);
	const names = db.prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\\_%' ESCAPE '\\'").all();
	const counts = Object.fromEntries(names.map(({ name }) => [name, db.prepare(`SELECT COUNT(*) AS n FROM "${name}"`).get().n]));
	db.close();
	return counts;
}

const fixturePath = (dir, turns) => join(dir, `pi-${turns}.sqlite`);
const sizesOf = (text) => (text ? text.split(",").map(Number) : SIZES);
const size = (path) => { try { return statSync(path).size; } catch { return 0; } };

async function seed(dir, sizes) {
	mkdirSync(dir, { recursive: true });
	const work = join(dir, "pi-work.sqlite");
	for (const suffix of ["", "-wal", "-shm"]) rmSync(work + suffix, { force: true });
	let done = 0;
	let digest = "";
	for (const target of sizes) {
		const started = performance.now();
		while (done < target) {
			const end = Math.min(done + BATCH, target);
			const app = await open(work);
			for (const t of history(done, end)) await runTurn(app, t);
			digest = fingerprint(app.state.seen);
			await app.harness.close(context);
			done = end;
			process.stderr.write(`\rpi ${done}/${target}`);
		}
		copyFileSync(work, fixturePath(dir, target));
		const meta = { target: "pi", turns: target, fingerprint: digest, seedMs: Math.round(performance.now() - started), bytes: size(work), tables: tables(work) };
		writeFileSync(`${fixturePath(dir, target)}.json`, JSON.stringify(meta));
		process.stderr.write(`\r${JSON.stringify(meta)}\n`);
	}
	for (const suffix of ["", "-wal", "-shm"]) rmSync(work + suffix, { force: true });
}

const cpuSeconds = () => { const u = process.cpuUsage(); return (u.user + u.system) / 1e6; };
const rssMB = () => process.resourceUsage().maxRSS / 1024;

async function sample(fixture, turns, index) {
	const dir = mkdtempSync(join(tmpdir(), "durableperf-"));
	const work = join(dir, "store.sqlite");
	copyFileSync(fixture, work);
	const cpuStart = cpuSeconds();
	let at = performance.now();
	const app = await open(work);
	const openMs = performance.now() - at;
	const openCpu = 1000 * (cpuSeconds() - cpuStart);
	const out = [];
	const turnCpu = [];
	for (let i = 0; i < MEASURED_TURNS; i++) {
		at = performance.now();
		const turnCpuStart = cpuSeconds();
		if (process.env.COUNT) for (const key of Object.keys(counters)) counters[key] = 0;
		await runTurn(app, turn(`m${i}`, MEASURED_TOOLS));
		out.push(performance.now() - at);
		turnCpu.push(1000 * (cpuSeconds() - turnCpuStart));
		if (process.env.COUNT && i === 3) console.error(JSON.stringify({ turns, counters }));
	}
	const cpu = cpuSeconds() - cpuStart;
	const rss = rssMB();
	const finger = fingerprint(app.state.seen);
	await app.harness.close(context);
	const line = { target: "pi", version: piDurableVersion, turns, sample: index, open: openMs, turn: out, openCpu, turnCpu, rss, peakRss: rss, cpuSeconds: cpu, bytes: size(work), tables: tables(work), fingerprint: finger, at: new Date().toISOString() };
	rmSync(dir, { recursive: true, force: true });
	console.log(JSON.stringify(line));
}

const median = (v) => { const s = [...v].sort((a, b) => a - b); const m = s.length >> 1; return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2; };
const p95 = (v) => { const s = [...v].sort((a, b) => a - b); return s[Math.min(s.length - 1, Math.floor(s.length * 0.95))]; };

function run(dir, sizes, samples, out) {
	console.log(["turns", "cold ms", "warm ms", "warm p95", "open ms", "MB", "rss MB", "cpu s"].map((h, i) => (i ? h.padStart(9) : h.padStart(7))).join(" "));
	for (const turns of sizes) {
		const cold = [], warm = [], all = [], opens = [], rss = [], cpu = [], bytes = [];
		for (let i = 0; i < samples; i++) {
			const result = spawnSync(process.execPath, ["--no-warnings", fileURLToPath(import.meta.url), "sample", fixturePath(dir, turns), String(turns), String(i)], { encoding: "utf8", maxBuffer: 1 << 26 });
			if (result.status !== 0) throw new Error(`sample ${turns}/${i}: ${result.stderr}`);
			appendFileSync(out, result.stdout);
			const r = JSON.parse(result.stdout);
			cold.push(r.open + r.turn[0]); warm.push(median(r.turn.slice(1))); all.push(...r.turn.slice(1));
			opens.push(r.open); rss.push(r.peakRss); cpu.push(r.cpuSeconds); bytes.push(r.bytes / 1e6);
		}
		const f = (v, d) => v.toFixed(d).padStart(9);
		console.log(`${String(turns).padStart(7)} ${f(median(cold), 0)} ${f(median(warm), 0)} ${f(p95(all), 0)} ${f(median(opens), 0)} ${f(median(bytes), 1)} ${f(median(rss), 0)} ${f(median(cpu), 2)}`);
	}
}

const [command, a, b, c] = process.argv.slice(2);
if (command === "seed") await seed(a, sizesOf(b));
else if (command === "sample") await sample(a, Number(b), Number(c ?? 0));
else if (command === "run") run(a, sizesOf(b), Number(c ?? 3), process.env.OUT ?? "results.jsonl");
else { console.error("usage: pi.mjs seed|sample|run"); process.exit(2); }
