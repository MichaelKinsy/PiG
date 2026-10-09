// Regenerates pi_array_root_operations.json: Pi's tracker.ts operations for seeded edit scripts over ARRAY roots
// (track([...])). Run from the repository root with Node 24 or later:
//
//	node chord/delta/testdata/pi_array_root_operations.mjs > chord/delta/testdata/pi_array_root_operations.json
import { track } from "../../../.upstream/current/packages/chord/src/delta/index.ts";

function mulberry32(seed) {
	let state = seed >>> 0;
	return () => {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}
const clone = (value) => JSON.parse(JSON.stringify(value));
const pick = (random, items) => items[Math.floor(random() * items.length)];
const digit = (random) => Math.floor(random() * 10);

// Two root shapes: numbers, and rows holding a nested array.
function initial(random, rows) {
	const length = Math.floor(random() * 7);
	return Array.from({ length }, (_, id) => (rows ? { id, value: digit(random), tags: Array.from({ length: Math.floor(random() * 3) }, () => digit(random)) } : digit(random)));
}
const element = (random, rows) => (rows ? { id: 100 + Math.floor(random() * 50), value: digit(random), tags: [] } : digit(random));

function step(random, rows) {
	const kind = pick(random, ["push", "unshift", "shift", "pop", "splice", "reverse", "sort", "setIndex", "setIndex", "rowField", "tag", "length"]);
	switch (kind) {
		case "push": return ["push", [element(random, rows), ...(random() < 0.5 ? [element(random, rows)] : [])]];
		case "unshift": return ["unshift", [element(random, rows)]];
		case "splice": return ["splice", Math.floor(random() * 4) - 1, Math.floor(random() * 3), random() < 0.6 ? [element(random, rows)] : []];
		case "setIndex": return ["setIndex", Math.floor(random() * 4), element(random, rows)];
		case "rowField": return ["rowField", Math.floor(random() * 4), pick(random, ["value", "id"]), digit(random)];
		case "tag": return ["tag", Math.floor(random() * 4), digit(random)];
		case "length": return ["length", Math.floor(random() * 4)];
		default: return [kind];
	}
}

function run(state, rows, [kind, ...rest]) {
	switch (kind) {
		case "push": state.push(...clone(rest[0])); return true;
		case "unshift": state.unshift(...clone(rest[0])); return true;
		case "shift": state.shift(); return true;
		case "pop": state.pop(); return true;
		case "splice": state.splice(rest[0], rest[1], ...clone(rest[2])); return true;
		case "reverse": state.reverse(); return true;
		case "sort": state.sort(rows ? (a, b) => a.value - b.value || a.id - b.id : (a, b) => a - b); return true;
		case "setIndex": if (rest[0] > state.length) return false; state[rest[0]] = clone(rest[1]); return true;
		case "rowField": if (!rows || state[rest[0]] === undefined) return false; state[rest[0]][rest[1]] = rest[2]; return true;
		case "tag": if (!rows || state[rest[0]] === undefined) return false; state[rest[0]].tags.push(rest[1]); return true;
		case "length": if (rest[0] > state.length) return false; state.length = rest[0]; return true;
	}
	return false;
}

const cases = [];
for (let seed = 1; seed <= 800; seed++) {
	const random = mulberry32(seed);
	const rows = seed % 2 === 0;
	const base = initial(random, rows);
	const steps = Array.from({ length: 1 + Math.floor(random() * 8) }, () => step(random, rows));
	const change = track(clone(base)).beginChange();
	const applied = [];
	for (const edit of steps) {
		try {
			if (run(change.state, rows, edit)) applied.push(edit);
		} catch {
			// A step Pi rejects is dropped from the case.
		}
	}
	const prepared = change.prepare();
	cases.push({ seed, rows, initial: base, steps: applied, ops: clone(prepared.ops), value: clone(prepared.value) });
}
process.stdout.write("[\n" + cases.map((entry) => JSON.stringify(entry)).join(",\n") + "\n]\n");
