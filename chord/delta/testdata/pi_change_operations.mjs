// Regenerates pi_change_operations.json: Pi's tracker.ts operations for seeded random edit scripts.
// Run from the repository root with Node 24 or later:
//
//	node chord/delta/testdata/pi_change_operations.mjs > chord/delta/testdata/pi_change_operations.json
//	node chord/delta/testdata/pi_change_operations.mjs --folds > chord/delta/testdata/pi_change_operations_folds.json
//
// --folds adds edits below reserved keys (__proto__, constructor, prototype), which tracker.ts folds into one set at
// the nearest safe ancestor, and runs of row edits that fold into dense-region splices, so the emission order of
// folded and dense nodes relative to dirty ones is recorded too.
//
// Each case is an initial JSON value, a script of draft edits and the exact operation batch
// Pi's tracker records for it (packages/chord/src/delta, the pinned mirror under .upstream/current).
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
const FOLDS = process.argv.includes("--folds");

function initial(random) {
	const rows = (n) => Array.from({ length: n }, (_, id) => ({ id, value: Math.floor(random() * 10) }));
	return {
		count: Math.floor(random() * 10),
		name: "n" + Math.floor(random() * 10),
		list: Array.from({ length: Math.floor(random() * 6) }, () => Math.floor(random() * 10)),
		rows: rows(FOLDS && random() < 0.15 ? 256 + Math.floor(random() * 40) : Math.floor(random() * 5)),
		nested: { a: 1, b: { c: "x", items: [1, 2, 3] } },
		...(FOLDS ? JSON.parse('{"guard":{"constructor":{"x":1,"prototype":{"y":2}}},"deep":{"k":{"__proto__":{"z":1},"w":1}}}') : {}),
	};
}

// Paths to arrays and objects that exist in the generated documents.
const ARRAYS = [["list"], ["rows"], ["nested", "b", "items"]];

function pick(random, items) {
	return items[Math.floor(random() * items.length)];
}

function value(random, rows) {
	return rows ? { id: 100 + Math.floor(random() * 50), value: Math.floor(random() * 10) } : Math.floor(random() * 10);
}

function step(random) {
	const kind = pick(random, ["set", "set", "delete", "push", "unshift", "shift", "pop", "splice", "reverse", "sort", "setIndex", "append", "rowField", "rowField", "text"]);
	const path = pick(random, ARRAYS);
	const rows = path[0] === "rows";
	switch (kind) {
		case "set": return pick(random, [
			["set", ["count"], Math.floor(random() * 10)],
			["set", ["name"], "m" + Math.floor(random() * 10)],
			["set", ["nested", "a"], Math.floor(random() * 10)],
			["set", ["nested", "b", "c"], "y" + Math.floor(random() * 10)],
			["set", ["added", String(Math.floor(random() * 3))], Math.floor(random() * 10)],
			...(FOLDS
				? [
						["set", ["guard", "constructor", "x"], Math.floor(random() * 10)],
						["set", ["guard", "constructor", "prototype", "y"], Math.floor(random() * 10)],
						["set", ["deep", "k", "__proto__", "z"], Math.floor(random() * 10)],
						["set", ["deep", "k", "w"], Math.floor(random() * 10)],
						["set", ["nested", "prototype"], Math.floor(random() * 10)],
					]
				: []),
		]);
		case "delete": return ["delete", pick(random, [["count"], ["name"], ["nested", "a"], ["added"]])];
		case "push": return ["push", path, [value(random, rows), ...(random() < 0.5 ? [value(random, rows)] : [])]];
		case "unshift": return ["unshift", path, [value(random, rows)]];
		case "shift": return ["shift", path];
		case "pop": return ["pop", path];
		case "splice": return ["splice", path, Math.floor(random() * 4) - 1, Math.floor(random() * 3), random() < 0.6 ? [value(random, rows)] : []];
		case "reverse": return ["reverse", path];
		case "sort": return ["sort", path];
		case "setIndex": return ["setIndex", path, Math.floor(random() * 3), value(random, rows)];
		case "rowField": return ["rowField", ["rows"], Math.floor(random() * 4), pick(random, ["value", "id", "extra"]), Math.floor(random() * 10)];
		case "text": return ["text", pick(random, [["name"], ["nested", "b", "c"]]), pick(random, ["append", "cut", "replace"]), "s" + Math.floor(random() * 10)];
		default: return ["append", path, "value", Math.floor(random() * 10)];
	}
}

function at(root, path) {
	return path.reduce((target, key) => target?.[key], root);
}

function run(state, [kind, path, ...rest]) {
	if (kind === "set" || kind === "delete") {
		const parent = path.length === 1 ? state : at(state, path.slice(0, -1));
		const key = path.at(-1);
		if (parent === undefined || typeof parent !== "object") return false;
		if (kind === "set") parent[key] = clone(rest[0]);
		else delete parent[key];
		return true;
	}
	if (kind === "rowRange") {
		for (let index = rest[0]; index < rest[1]; index++) state.rows[index].value = rest[2] + (index % 7);
		return true;
	}
	if (kind === "rowField") {
		const row = state.rows[rest[0]];
		if (row === undefined) return false;
		row[rest[1]] = rest[2];
		return true;
	}
	if (kind === "text") {
		const parent = path.length === 1 ? state : at(state, path.slice(0, -1));
		const key = path.at(-1);
		const current = parent?.[key];
		if (typeof current !== "string") return false;
		parent[key] = rest[0] === "append" ? current + rest[1] : rest[0] === "cut" ? current.slice(0, Math.max(current.length - 1, 0)) + rest[1] : rest[1];
		return true;
	}
	const target = at(state, path);
	if (!Array.isArray(target)) return false;
	const rows = path[0] === "rows";
	switch (kind) {
		case "push": target.push(...clone(rest[0])); return true;
		case "unshift": target.unshift(...clone(rest[0])); return true;
		case "shift": target.shift(); return true;
		case "pop": target.pop(); return true;
		case "splice": target.splice(rest[0], rest[1], ...clone(rest[2])); return true;
		case "reverse": target.reverse(); return true;
		case "sort": target.sort(rows ? (a, b) => a.value - b.value || a.id - b.id : (a, b) => a - b); return true;
		case "setIndex": if (rest[0] > target.length) return false; target[rest[0]] = clone(rest[1]); return true;
		case "append": if (target.length === 0) return false; target[0] = rows ? { ...target[0], value: rest[1] } : rest[1]; return true;
	}
	return false;
}

const cases = [];
for (let seed = 1; seed <= (FOLDS ? 200 : 1500); seed++) {
	const random = mulberry32(seed);
	const base = initial(random);
	const steps = Array.from({ length: 1 + Math.floor(random() * 10) }, () => step(random));
	if (base.rows.length >= 256) {
		// Two runs of row edits; together they mark enough rows to fold into a dense-region splice.
		const start = Math.floor(random() * 20);
		const middle = start + 100 + Math.floor(random() * 60);
		const end = Math.min(base.rows.length, start + 256 + Math.floor(random() * 20));
		steps.splice(Math.floor(random() * (steps.length + 1)), 0, ["rowRange", ["rows"], start, middle, 10 + Math.floor(random() * 10)]);
		steps.splice(Math.floor(random() * (steps.length + 1)), 0, ["rowRange", ["rows"], middle, end, 20 + Math.floor(random() * 10)]);
	}
	const change = track(clone(base)).beginChange();
	const applied = [];
	for (const edit of steps) {
		try {
			if (run(change.state, edit)) applied.push(edit);
		} catch {
			// A step Pi rejects (for example a hole) is dropped from the case.
		}
	}
	const prepared = change.prepare();
	cases.push({ seed, initial: base, steps: applied, ops: clone(prepared.ops), value: clone(prepared.value) });
}
process.stdout.write("[\n" + cases.map((entry) => JSON.stringify(entry)).join(",\n") + "\n]\n");
