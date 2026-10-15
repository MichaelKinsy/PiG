// Oracle for the usage ledger (src/harness/usage.ts). Lines:
//   A <hex total> <hex usage> <hex total after addUsage>
//   B <hex doc state> <bucket> <key> <hex usage> <hex doc state after recordUsage>
//   S <hex sum> <hex state> <hex sum after addUsageState>
import { writeFileSync } from "node:fs";
import { PI } from "./pimain.mjs";
import { rng, hex } from "./rng.mjs";

const u = await import(`${PI}/packages/durable/src/harness/usage.ts`);
const r = rng(Number(process.argv[2] ?? 5));
const out = process.argv[3] ?? new URL("../testdata/usage.hex", import.meta.url).pathname;

const num = () => r.pick([0, 1, 2, 7, 0.5, 1.25, 1e21, 123456789.125, 0.1, 0.2]);
function usage(opt = true) {
	const x = { input: num(), output: num(), cacheRead: num(), cacheWrite: num(), totalTokens: num() };
	if (opt && r.chance(0.4)) x.cacheWrite1h = num();
	if (opt && r.chance(0.4)) x.reasoning = num();
	x.cost = { input: num(), output: num(), cacheRead: num(), cacheWrite: num(), total: num() };
	if (r.chance(0.3)) { const { cost, ...rest } = x; return { ...rest, cost }; }
	return x;
}
const keys = ["a/m", "b/n", "read", "toString", "1", "0", "constructor", "x y"];
const lines = [];
const J = (v) => JSON.stringify(v);
for (let i = 0; i < 120; i++) {
	const before = usage();
	const total = JSON.parse(J(before));
	const add = usage();
	u.addUsage(total, JSON.parse(J(add)));
	lines.push(`A ${hex(J(before))} ${hex(J(add))} ${hex(J(total))}`);
}
function state() {
	const bucket = () => { const b = {}; for (let i = r.int(4); i > 0; i--) b[r.pick(keys)] = usage(); return b; };
	return { models: bucket(), tools: bucket() };
}
for (let i = 0; i < 120; i++) {
	const before = state();
	const doc = JSON.parse(J(before));
	const bucket = r.pick(["models", "tools"]);
	const key = r.pick(keys);
	const add = usage();
	const tx = { doc: async () => doc };
	await u.recordUsage(tx, 1, bucket, key, JSON.parse(J(add)));
	lines.push(`B ${hex(J(before))} ${bucket} ${hex(key)} ${hex(J(add))} ${hex(J(doc))}`);
}
for (let i = 0; i < 120; i++) {
	const sumBefore = state();
	const sum = JSON.parse(J(sumBefore));
	const s = state();
	u.addUsageState(sum, JSON.parse(J(s)));
	lines.push(`S ${hex(J(sumBefore))} ${hex(J(s))} ${hex(J(sum))}`);
}
writeFileSync(out, lines.join("\n") + "\n");
console.log(lines.length, "lines");
