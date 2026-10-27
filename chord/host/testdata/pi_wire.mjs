// Regenerates pi_wire.json: Pi's packages/chord/src/wire.ts (isFrame, toWireError, fromWireError) run over the inputs
// below. Run from the repository root with Node 24 or later:
//
//	node chord/host/testdata/pi_wire.mjs > chord/host/testdata/pi_wire.json
//
// frames are one valid frame of every type, each with its optional members, and seeded edits of them (a member dropped,
// replaced by a value of another type, a number off by one, an unknown member added, the type tag changed). Every case
// is JSON text and the verdict Pi's isFrame gave it; the Go test replays the text through chord/host.
import { ChordError } from "../../../.upstream/current/packages/chord/src/errors.ts";
import { fromWireError, isFrame, PROTOCOL_VERSION, toWireError } from "../../../.upstream/current/packages/chord/src/wire.ts";

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

const token = { name: "thing", scope: "remote", version: 1, input: { type: "object" }, host: { name: "h" }, variants: { "": { m: { kind: "method" }, s: { kind: "state", schema: { type: "number" } }, o: { kind: "opaque", id: "x" } } } };
const wireError = { code: "gone", message: "gone: x", reason: "r" };
const report = { codeVersion: 2, previous: 1, changed: ["a"], removed: [], prepared: ["a"], gated: [] };
const bases = [
	{ t: "hello", name: "h", kind: "host", codeVersion: 0 },
	{ t: "hello", protocol: PROTOCOL_VERSION, name: "h", kind: "client", codeVersion: 3, acks: true },
	{ t: "ack", n: 1 },
	{ t: "hold", id: 1, at: "h", token, input: {} },
	{ t: "hold", id: 2, at: "h", token, input: { a: 1 }, principal: { kind: "x" }, origin: "o", state: false, again: true },
	{ t: "release", id: 1 },
	{ t: "shape", id: 1, members: ["m", "s"], contract: "c", state: ["s"] },
	{ t: "shape", id: 1, variant: "v", members: [], contract: "c", state: [] },
	{ t: "gone", id: 1, error: wireError },
	{ t: "gone", id: 1, error: { code: "gone", message: "m" } },
	{ t: "call", id: 1, hold: 1, member: "m" },
	{ t: "call", id: 2, hold: 1, member: "m", args: [1, 2], root: { rootId: "r", codeVersion: 1 } },
	{ t: "result", id: 1 },
	{ t: "result", id: 1, value: { a: [1] } },
	{ t: "result", id: 1, error: wireError },
	{ t: "cancel", id: 1 },
	{ t: "sub", hold: 1 },
	{ t: "unsub", hold: 1 },
	{ t: "resync", hold: 1, m: "s" },
	{ t: "state", hold: 1, m: "s", as: "snap", value: 1 },
	{ t: "state", hold: 1, m: "s", as: "reset" },
	{ t: "state", hold: 1, m: "s", as: "ops", ops: [["s", ["a"], 1], ["d", ["b"]]] },
	{ t: "state", hold: 1, m: "s", as: "error", error: wireError },
	{ t: "ping", n: 0 },
	{ t: "pong", n: -4 },
	{ t: "bye" },
	{ t: "bye", reason: "protocol-version 4" },
	{ t: "root_end", rootId: "r" },
	{ t: "invalidate", pattern: { kind: "x", id: 1 } },
	{ t: "prepare", reload: 1, codeVersion: 2, plugins: ["a"], options: {} },
	{ t: "prepare", reload: 1, codeVersion: 2, plugins: [], options: { remove: ["a"], gateDeadline: 1.5, retireDeadline: 0 } },
	{ t: "vote", reload: 1 },
	{ t: "vote", reload: 1, error: wireError, report },
	{ t: "commit", reload: 3 },
	{ t: "retire", codeVersion: 1 },
	{ t: "abort", reload: 1 },
];

const random = mulberry32(20261024);
const pick = (list) => list[Math.floor(random() * list.length)];
const WRONG = [null, "x", "", 0, -1, 1, 1.5, true, false, [], {}, [1], { a: 1 }, 1e21];
const TAGS = bases.map((frame) => frame.t);
function paths(value, prefix = []) {
	const out = [prefix];
	if (value !== null && typeof value === "object" && !Array.isArray(value)) {
		for (const key of Object.keys(value)) if (prefix.length < 2) out.push(...paths(value[key], [...prefix, key]));
	}
	return out;
}
function edit(frame) {
	const copy = JSON.parse(JSON.stringify(frame));
	const options = paths(copy).filter((p) => p.length > 0);
	if (options.length === 0) return copy;
	const path = pick(options);
	let target = copy;
	for (const key of path.slice(0, -1)) target = target[key];
	const last = path[path.length - 1];
	const roll = random();
	if (roll < 0.25) delete target[last];
	else if (roll < 0.6) target[last] = pick(WRONG);
	else if (roll < 0.75 && typeof target[last] === "number") target[last] += pick([-1, 1, 0.5]);
	else if (roll < 0.85) target.extra = pick(WRONG);
	else if (last === "t") target.t = pick(TAGS);
	else target[last] = pick(WRONG);
	return copy;
}
const cases = [];
const seen = new Set();
const add = (value) => {
	const text = JSON.stringify(value);
	if (text === undefined || seen.has(text)) return;
	seen.add(text);
	cases.push({ text, expect: isFrame(JSON.parse(text)) });
};
for (const base of bases) add(base);
for (const value of [null, 1, "hello", [], [{ t: "ack", n: 1 }], {}, { t: 1 }, { t: "nope" }, { n: 1 }]) add(value);
for (let index = 0; index < 4000; index++) {
	let frame = pick(bases);
	frame = edit(frame);
	if (random() < 0.3) frame = edit(frame);
	add(frame);
}

const wireErrors = [];
const errors = [
	new ChordError("gone", "g"),
	new ChordError("invalid", "bad: args", { reason: "args" }),
	new ChordError("forbidden", "forbidden: nope", { reason: "r_x" }),
	new ChordError("superseded", ""),
	new Error("plain"),
	new TypeError("typed"),
	new AggregateError([new Error("a"), new Error("b")], "several"),
];
for (const error of errors) {
	const wire = toWireError(error);
	const back = fromWireError(wire);
	wireErrors.push({ kind: error instanceof ChordError ? "chord" : error instanceof AggregateError ? "aggregate" : "plain", code: error.code ?? null, message: error.message, reason: error.reason ?? null, wire, back: { name: back.name, message: back.message, code: back.code, reason: back.reason ?? null, retryable: back.retryable } });
}
const fromWire = [
	{ code: "gone", message: "gone: x" },
	{ code: "invalid", message: "invalid/args: x: y", reason: "args" },
	{ code: "unavailable", message: "Unavailable: x" },
	{ code: "unavailable", message: "a_b/c_d: x" },
	{ code: "unavailable", message: "a/b/c: x" },
	{ code: "unavailable", message: "x: a_b: y" },
	{ code: "unavailable", message: "ab:x" },
	{ code: "aborted", message: "aborted: aborted: again" },
].map((wire) => {
	const back = fromWireError(wire);
	return { wire, back: { name: back.name, message: back.message, code: back.code, reason: back.reason ?? null, retryable: back.retryable } };
});

process.stdout.write(`${JSON.stringify({ protocol: PROTOCOL_VERSION, cases, wireErrors, fromWire }, null, "\t")}\n`);
