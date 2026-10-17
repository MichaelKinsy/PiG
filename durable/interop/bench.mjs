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
// BENCH_DURABLE_VERSION and BENCH_TARGET name another implementation of the same API that runs this program (the PiG
// core through durable/core/host/cf/bench/pig-durable.mjs).
const piDurableVersion = process.env.BENCH_DURABLE_VERSION ?? JSON.parse(readFileSync(new URL("../package.json", import.meta.resolve("@earendil-works/pi-durable")), "utf8")).version;
const benchTarget = process.env.BENCH_TARGET ?? "pi";

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
// BENCH_MEASURED_TURNS: the measured turns of one sample, 10 when unset (durable-bench's). The benchmark's gated numbers use
// 10; a longer sample serves a reported steady-state window (turns 20-29 of 30), whose transcript, store and memory differ
// from the gated sample's. A sample needs a cold turn and a warm one.
const MEASURED_TURNS = (() => {
	const v = process.env.BENCH_MEASURED_TURNS;
	if (v === undefined || v === "") return 10;
	const n = Number(v);
	if (!/^[0-9]+$/.test(v) || n < 2) throw new Error(`BENCH_MEASURED_TURNS ${JSON.stringify(v)}: want an integer of at least 2`);
	return n;
})();
const MEASURED_TOOLS = 8;
// BENCH_WORKLOAD: "standard" (unset; durable-bench's workload, compaction off) or "summary", the realistic-summary case.
// summary keeps the same turns and turn counts and turns compaction on with pi-durable's defaults. It uses a 200,000-token
// window and 8 KiB lookup results (the contract's W4a shape), so a store compacts about every 150 turns and a measured turn
// about every eighth. The scripted model answers the summarization request with plain text of a typical Pi summary's
// length (SUMMARY), never with a tool call. A model that calls a tool there makes every compaction fail and retry, which
// is the stress case, not this one.
// "summary-blocking" is summary with compaction only ever blocking (backgroundTokens 0). summary's compaction may start in the
// background beside the next request, so which later model call first sees the summary depends on timing, and a host with
// another scheduling (native goroutines) writes another transcript; blocking compaction finishes before the next request on
// every host, so the transcripts are equal.
const WORKLOAD = (() => {
	const w = process.env.BENCH_WORKLOAD ?? "";
	if (w === "" || w === "standard") return "standard";
	if (w === "summary" || w === "summary-blocking") return w;
	throw new Error(`BENCH_WORKLOAD ${JSON.stringify(w)}: want standard, summary or summary-blocking`);
})();
const SUMMARIZES = WORKLOAD !== "standard";
const SUMMARY_WINDOW = 200_000;
const SUMMARY_PAYLOAD_KB = 8;
// 23,511 characters: the median length of the 1,417 summaries with Pi's template (## Goal ... ## Progress) in the coding
// agent sessions on the measuring host (interquartile range 18,602-29,017), so the history carries summaries of the size Pi
// writes. Deterministic, the same bytes on every target.
const SUMMARY_CHARS = 23_511;
const SUMMARY = (() => {
	let text = "## Goal\nRun the scripted benchmark conversation: call lookup as instructed, then answer briefly.\n\n## Constraints & Preferences\n- Use the lookup tool exactly as each turn asks.\n\n## Progress\n### Done\n";
	for (let i = 1; text.length < SUMMARY_CHARS; i++) text += `- [x] Turn ${i}: called lookup, read record ${i}, and answered with the count of lookups done.\n`;
	return text.slice(0, SUMMARY_CHARS);
})();
const SUMMARIZATION_SYSTEM = "You are a context summarization assistant";
const BATCH = 50;
const SIZES = [50, 250, 1000, 3500];
const SYSTEM = "You are a benchmark agent. Call lookup as instructed, then answer briefly.";

const turn = (id, tools) => ({ id, text: `turn ${id} tools=${tools}` });
const history = (from, to) => Array.from({ length: to - from }, (_, i) => turn(`h${from + i}`, CYCLE[(from + i) % CYCLE.length]));
const payload = (n) => {
	const head = `record ${n}: `;
	return head + "x".repeat((SUMMARIZES ? SUMMARY_PAYLOAD_KB * 1024 : n % 97 === 0 ? 8192 : 256) - head.length);
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
	const faux = fauxProvider({ models: [{ id: "scripted-1", contextWindow: SUMMARIZES ? SUMMARY_WINDOW : 1e9 }], tokenSize: { min: 1e9, max: 1e9 } });
	const models = createModels();
	models.setProvider(faux.provider);
	const state = { seen: [] };
	const respond = (transcript) => {
		faux.appendResponses([respond]);
		// The summarization request is not a scripted turn: answer it with the summary text and leave the script's state alone
		// (durable/contract/lib/workload.mjs does the same).
		if (SUMMARIZES && transcript.messages.some((m) => m.role === "system" && (typeof m.content === "string" ? m.content : m.content.map((b) => b.text ?? "").join("")).startsWith(SUMMARIZATION_SYSTEM)))
			return fauxAssistantMessage(SUMMARY);
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
		// The result's text for the model: `output` from Pi's 42a3497d on, `content` in 1.1.0 and the durable contract's
		// main (durable/contract/PIN), which durable-core runs this file against. Each ignores the other's field.
		execute: async ({ n }) => {
			const text = [{ type: "text", text: payload(n) }];
			return { output: text, content: text };
		},
	});
	const registry = createRegistry();
	registry.install(defineExtension({ name: "bench", tools: [lookup], sections: [section("preamble", () => SYSTEM, { tag: false })] }));
	const harness = await Harness.open(await openNodeSqliteStorage(path), { models, registry, settings: { compaction: { enabled: SUMMARIZES, ...(WORKLOAD === "summary-blocking" ? { backgroundTokens: 0 } : {}) } } }, context);
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
		const meta = { target: benchTarget, ...(WORKLOAD === "standard" ? {} : { workload: WORKLOAD }), turns: target, fingerprint: digest, seedMs: Math.round(performance.now() - started), bytes: size(work), tables: tables(work) };
		writeFileSync(`${fixturePath(dir, target)}.json`, JSON.stringify(meta));
		process.stderr.write(`\r${JSON.stringify(meta)}\n`);
	}
	for (const suffix of ["", "-wal", "-shm"]) rmSync(work + suffix, { force: true });
}

const cpuSeconds = () => { const u = process.cpuUsage(); return (u.user + u.system) / 1e6; };
// The main thread's CPU in ms (process.threadCpuUsage): the thread that runs the JavaScript and the Wasm core. cpuUsage
// adds V8's background threads (compilation, concurrent GC).
const mainCpuMs = () => { const u = process.threadCpuUsage(); return (u.user + u.system) / 1e3; };
// The peak resident set in MiB. On Linux it is VmHWM, the high-water mark of this process own address space:
// resourceUsage().maxRSS (getrusage) also carries the peak of the address space the process was exec-d from (the launcher
// when it spawns with vfork) and lags VmHWM. Darwin maxRSS starts afresh at exec.
const rssMB = () =>
	process.platform === "linux"
		? Number(/^VmHWM:\s+(\d+) kB$/m.exec(readFileSync("/proc/self/status", "utf8"))[1]) / 1024
		: process.resourceUsage().maxRSS / 1024;

async function sample(fixture, turns, index) {
	const dir = mkdtempSync(join(tmpdir(), "durableperf-"));
	const work = join(dir, "store.sqlite");
	copyFileSync(fixture, work);
	const cpuStart = cpuSeconds();
	const mainStart = mainCpuMs();
	let at = performance.now();
	const app = await open(work);
	const openMs = performance.now() - at;
	// Main-thread readings nest inside the process readings (start after, end before), so a main figure never exceeds its
	// process figure.
	const openMainCpu = mainCpuMs() - mainStart;
	const openCpu = 1000 * (cpuSeconds() - cpuStart);
	const out = [];
	const turnCpu = [];
	const turnMainCpu = [];
	for (let i = 0; i < MEASURED_TURNS; i++) {
		at = performance.now();
		const turnCpuStart = cpuSeconds();
		const turnMainStart = mainCpuMs();
		if (process.env.COUNT) for (const key of Object.keys(counters)) counters[key] = 0;
		await runTurn(app, turn(`m${i}`, MEASURED_TOOLS));
		out.push(performance.now() - at);
		turnMainCpu.push(mainCpuMs() - turnMainStart);
		turnCpu.push(1000 * (cpuSeconds() - turnCpuStart));
		if (process.env.COUNT && i === 3) console.error(JSON.stringify({ turns, counters }));
	}
	const cpu = cpuSeconds() - cpuStart;
	const rss = rssMB();
	const finger = fingerprint(app.state.seen);
	// retained: the memory the open store keeps after a forced collection, in MiB: the JS heap plus external memory, which
	// holds a Wasm core's linear memory (SQLite's own allocations are in neither). After every timed span; only when Node
	// runs with --expose-gc.
	let retained;
	if (typeof globalThis.gc === "function") {
		globalThis.gc();
		const m = process.memoryUsage();
		retained = (m.heapUsed + m.external) / 1048576;
	}
	await app.harness.close(context);
	const line = { target: benchTarget, version: piDurableVersion, turns, sample: index, ...(WORKLOAD === "standard" ? {} : { workload: WORKLOAD }), open: openMs, turn: out, openCpu, turnCpu, openMainCpu, turnMainCpu, rss, peakRss: rss, ...(retained === undefined ? {} : { retained }), cpuSeconds: cpu, bytes: size(work), tables: tables(work), fingerprint: finger, at: new Date().toISOString() };
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
