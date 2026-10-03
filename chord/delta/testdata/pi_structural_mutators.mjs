// Regenerates pi_structural_mutators.json from Pi's own tracker (packages/chord/src/delta, the pinned mirror under
// .upstream/current) with the native-vs-draft comparison of tracker.test.ts "matches native structural coercion
// ordering for splice, fill, and copyWithin". Run from the repository root with Node 24 or later:
//
//	node chord/delta/testdata/pi_structural_mutators.mjs > chord/delta/testdata/pi_structural_mutators.json
//
// Each row records the native Array.prototype result and the result of the same call on a Pi draft of the same array,
// settled through prepare, replay of the operations, and adopt. A row whose draft call throws records the error and
// the unchanged committed array.
import { apply, track } from "../../../.upstream/current/packages/chord/src/delta/index.ts";

const base = () => [1, 2, 3];
const clone = (value) => JSON.parse(JSON.stringify(value));
// JSON has no NaN, so a NaN argument or element is recorded as the string "NaN".
const recordable = (values) => values.map((value) => (Number.isNaN(value) ? "NaN" : value));

const rows = [];
const splice = [
	[1, 1, [9]],
	[0, 0, []],
	[1, 0, []],
	[3, 0, [9, 8]],
	[0, 10, []],
	[-1, 1, [9]],
	[-2, 5, []],
	[-3, 0, [7]],
	[-5, 1, [9]],
	[5, 1, [9]],
	[1, -1, [9]],
	[2, 1, []],
];
for (const [start, deleteCount, items] of splice) rows.push({ method: "splice", args: [start, deleteCount, ...items] });
const fill = [
	[7, 0, 3],
	[7, 1, 2],
	[7, -2, -1],
	[7, -5, 2],
	[7, 1, 10],
	[7, 2, 1],
	[7, 1, 1],
	[7, 5, 9],
	[7, -1, 3],
	[Number.NaN, 0, 3],
	[Number.NaN, 1, 1],
	[Number.NaN, 2, 1],
];
for (const args of fill) rows.push({ method: "fill", args });
const copyWithin = [
	[0, 1, 3],
	[1, 0, 2],
	[2, 0, 1],
	[-1, 0, 1],
	[0, -2, -1],
	[0, -5, 1],
	[5, 0, 1],
	[0, 2, 1],
	[0, 1, 1],
	[1, 0, 10],
	[-5, -1, 3],
	[0, 3, 5],
];
for (const args of copyWithin) rows.push({ method: "copyWithin", args });

const output = [];
for (const { method, args } of rows) {
	const native = base();
	const nativeReturn = native[method](...args);
	const tracker = track({ values: base() });
	const change = tracker.beginChange();
	const row = { method, args: recordable(args), native: recordable(native) };
	if (method === "splice") row.nativeRemoved = nativeReturn;
	try {
		const draftReturn = change.state.values[method](...args);
		const prepared = change.prepare();
		const replayed = apply(clone(tracker.value), clone(prepared.ops));
		if (JSON.stringify(replayed) !== JSON.stringify(prepared.value)) throw new Error(`${method} replay diverged`);
		tracker.adopt(prepared);
		row.draft = tracker.value.values;
		if (method === "splice") row.draftRemoved = clone(draftReturn);
	} catch (error) {
		change.abort();
		row.draftError = `${error.constructor.name}: ${error.message}`;
		row.draft = tracker.value.values;
	}
	output.push(row);
}
process.stdout.write(`${JSON.stringify({ upstream: "packages/chord/src/delta (Pi 1.0.0)", rows: output }, null, "\t")}\n`);
