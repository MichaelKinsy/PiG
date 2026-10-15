// Drive the installed Pi chord tracker, never a translated oracle: seeded random edit scripts that place draft handles (an object or array of the same draft, possibly edited before and after the placement), fill, copyWithin and length writes, mixed with ordinary edits. Each case records every step with whether Pi threw, the prepared operations and the prepared value; the Go test replays the steps on its tracker and compares.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../.upstream/current/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { track } = await import(pathToFileURL(root + "dist/delta/index.js"));
const count = Number(process.argv[3]);
let seed = Number(process.argv[4]);

// mulberry32
const random = () => {
	seed = (seed + 0x6d2b79f5) | 0;
	let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
	t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const int = (n) => Math.floor(random() * n);
const pick = (items) => items[int(items.length)];
const clone = (value) => JSON.parse(JSON.stringify(value));

function initial() {
	return {
		count: int(10),
		list: Array.from({ length: int(6) }, () => int(10)),
		rows: Array.from({ length: int(5) }, (_, id) => ({ id, value: int(10) })),
		nested: { a: 1, b: { c: "x", items: [1, 2, 3] } },
	};
}

const ARRAYS = [["list"], ["rows"], ["nested", "b", "items"], ["moved"], ["nested", "copy"]];
const OBJECTS = [[], ["nested"], ["nested", "b"], ["moved"], ["nested", "copy"]];
// A source path starting with "@other" reads the draft of a second open change of the same tracker, edited before the script runs.
const SOURCES = [["@other", "rows"], ["@other", "nested", "b"], ["@other", "rows", 0], ["rows"], ["list"], ["nested"], ["nested", "b"], ["nested", "b", "items"], ["moved"], ["nested", "copy"], ["rows", 0], ["rows", 1], ["rows", 2]];
const plain = () => pick([int(10), "s" + int(10), { id: 100 + int(50), value: int(10) }, [int(10), int(10)], null]);

function step() {
	switch (pick(["place", "place", "push", "setIndex", "splice", "fill", "copyWithin", "length", "set", "delete", "rowField", "reverse", "edit", "edit", "unshift", "shift", "pop", "sort"])) {
		case "place": return ["place", pick(OBJECTS), pick(["moved", "copy", "a", "count", "c"]), pick(SOURCES)];
		case "push": return ["push", pick(ARRAYS), pick(SOURCES)];
		case "setIndex": return ["setIndex", pick(ARRAYS), int(4), pick(SOURCES)];
		case "splice": return ["splice", pick(ARRAYS), int(5) - 2, int(3), pick(SOURCES)];
		case "fill": return ["fill", pick(ARRAYS), random() < 0.5 ? { value: plain() } : { source: pick(SOURCES) }, int(6) - 3, int(7) - 2];
		case "copyWithin": return ["copyWithin", pick(ARRAYS), int(5) - 2, int(5) - 2, int(6) - 1];
		case "length": return ["length", pick(ARRAYS), int(6)];
		case "set": return ["set", pick(OBJECTS), pick(["moved", "copy", "a", "count", "c", "x"]), plain()];
		case "delete": return ["delete", pick(OBJECTS), pick(["moved", "copy", "a", "count", "c", "b"])];
		case "rowField": return ["rowField", pick(ARRAYS), int(4), pick(["value", "id", "extra"]), int(10)];
		case "reverse": return ["reverse", pick(ARRAYS)];
		case "unshift": return ["unshift", pick(ARRAYS), pick(SOURCES)];
		case "shift": return ["shift", pick(ARRAYS)];
		case "pop": return ["pop", pick(ARRAYS)];
		case "sort": return ["sort", pick(ARRAYS)];
		default: return ["edit", pick(SOURCES), pick(["value", "c", "id"]), int(10)];
	}
}

let other;
function at(state, path) {
	let target = state;
	if (path[0] === "@other") {
		target = other;
		path = path.slice(1);
	}
	for (const key of path) {
		if (target === null || typeof target !== "object") return undefined;
		target = target[key];
	}
	return target;
}

// sortKey orders mixed elements: a number by itself, an object by its numeric value field, anything else as 0.
const sortKey = (value) => (typeof value === "number" ? value : value !== null && typeof value === "object" && typeof value.value === "number" ? value.value : 0);
const isArray = (value) => Array.isArray(value);
const isObject = (value) => value !== null && typeof value === "object" && !Array.isArray(value);

// run returns false when the step does not apply to the current shape (a path that is not there), so it is dropped.
function run(state, [kind, path, ...rest]) {
	switch (kind) {
		case "place": case "set": case "delete": {
			const parent = at(state, path);
			if (!isObject(parent)) return false;
			if (kind === "delete") delete parent[rest[0]];
			else if (kind === "set") parent[rest[0]] = clone(rest[1]);
			else {
				const source = at(state, rest[1]);
				if (source === null || typeof source !== "object") return false;
				parent[rest[0]] = source;
			}
			return true;
		}
		case "edit": {
			const target = at(state, path);
			if (!isObject(target)) return false;
			target[rest[0]] = rest[1];
			return true;
		}
	}
	const target = at(state, path);
	if (!isArray(target)) return false;
	const source = (sourcePath) => {
		const value = at(state, sourcePath);
		return value !== null && typeof value === "object" ? value : undefined;
	};
	switch (kind) {
		case "push": { const value = source(rest[0]); if (value === undefined) return false; target.push(value); return true; }
		case "setIndex": { const value = source(rest[1]); if (value === undefined) return false; target[rest[0]] = value; return true; }
		case "splice": { const value = source(rest[2]); if (value === undefined) return false; target.splice(rest[0], rest[1], value); return true; }
		case "fill": {
			const value = rest[0].source === undefined ? clone(rest[0].value) : source(rest[0].source);
			if (value === undefined) return false;
			target.fill(value, rest[1], rest[2]);
			return true;
		}
		case "copyWithin": target.copyWithin(rest[0], rest[1], rest[2]); return true;
		case "length": target.length = rest[0]; return true;
		case "rowField": { const row = target[rest[0]]; if (!isObject(row)) return false; row[rest[1]] = rest[2]; return true; }
		case "reverse": target.reverse(); return true;
		case "unshift": { const value = source(rest[0]); if (value === undefined) return false; target.unshift(value); return true; }
		case "shift": target.shift(); return true;
		case "pop": target.pop(); return true;
		case "sort": target.sort((a, b) => sortKey(a) - sortKey(b)); return true;
	}
	throw new Error("unknown step " + kind);
}

const cases = [];
for (let index = 0; index < count; index++) {
	const base = initial();
	const tracker = track(clone(base));
	const change = tracker.beginChange();
	other = tracker.beginChange().state;
	if (other.rows.length > 0) other.rows[0].value = 99;
	other.nested.b.items.push(7);
	other.rows.unshift({ id: 50, value: 5 });
	const steps = [];
	for (let n = 1 + int(12); n > 0; n--) {
		const edit = step();
		try {
			if (run(change.state, edit)) steps.push({ edit, error: null });
		} catch (error) {
			steps.push({ edit, error: `${error.name}: ${error.message}` });
		}
	}
	const prepared = change.prepare();
	cases.push({ initial: base, steps, ops: clone(prepared.ops), value: clone(prepared.value) });
}
process.stdout.write(JSON.stringify(cases));
