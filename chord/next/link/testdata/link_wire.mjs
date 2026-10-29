// Drives Pi's own codec sources, never a translated oracle: copies binary.ts, text.ts, values.ts and part.ts of the
// pinned Pi tree (packages/chord/src/next/link) and encodes a fixed list of messages with binaryCodec and textCodec.
// Node strips the types. message.ts is not copied (it pulls in TypeBox and the contract layer): its two constants the
// codecs read, CAUSE_KINDS and NO_RETIRED, are extracted from it and asserted; part.ts and values.ts import only
// ChordError and invalid from the data layer, replaced by empty stand-ins the wire code never reaches.
//
//   node link_wire.mjs <pi tree root> > link_wire_golden.json
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.argv[2];
if (root === undefined) throw new Error("usage: link_wire.mjs <pi tree root>");
const source = join(root, "packages/chord/src/next/link");
const work = mkdtempSync(join(tmpdir(), "link-wire-"));
const out = join(work, "next/link");
mkdirSync(out, { recursive: true });
mkdirSync(join(work, "next/data"));
for (const file of ["binary.ts", "text.ts", "values.ts", "part.ts"]) cpSync(join(source, file), join(out, file));

const message = readFileSync(join(source, "message.ts"), "utf8");
const causeKinds = /export const CAUSE_KINDS: \["transient", "broken", "waiting"\] = \["transient", "broken", "waiting"\];/;
const noRetired = /export const NO_RETIRED: readonly number\[\] = \[\];/;
if (!causeKinds.test(message) || !noRetired.test(message)) throw new Error("message.ts constants changed");
writeFileSync(
	join(out, "message.ts"),
	'export const CAUSE_KINDS = ["transient", "broken", "waiting"];\nexport const NO_RETIRED = [];\n',
);
writeFileSync(join(work, "next/data/index.ts"), "export class ChordError extends Error {}\n");
writeFileSync(join(work, "next/data/internal.ts"), "export const invalid = (_r, m) => new Error(m);\n");

const { binaryCodec } = await import(pathToFileURL(join(out, "binary.ts")).href);
const { textCodec } = await import(pathToFileURL(join(out, "text.ts")).href);

const incarnation = "0190f5a2-7c3e-7d4a-9b1c-2d3e4f5a6b7c";
const other = "0190f5a2-7c3e-7d4a-9b1c-2d3e4f5a6b7d";
const server = { kind: "server", params: {} };
const chord = (code, message) => ({ kind: "chord", error: { code, message } });
const plugin = (code, message, details) => ({ kind: "plugin", code, message, ...(details === undefined ? {} : { details }) });
const hold = (extra = {}) => ({
	kind: "hold",
	hold: 0,
	type: "app.notes",
	version: 1,
	fingerprint: "0123456789abcdef",
	params: "{}",
	address: server,
	...extra,
});
const rep = (text, count) => ({ $repeat: [text, count] });
const json = (value) => ({ $json: value });
const deflated = (hex) => ({ $d: hex });
const raw = (text) => ({ $b: text });

/** The cases: parts are JSON text (a string), `{ $d: hex }` deflated, `{ $b: text }` bytes, `{ $json: s }` the JSON of
 * a string; `{ $repeat: [s, n] }` is a string of `s` n times anywhere; segments are hex strings. */
const cases = [
	["hello", { kind: "hello", protocol: 1, incarnation, calls: 256 }],
	["hold bare", hold({ hold: 300, version: 2, params: '{"id":1}' })],
	[
		"hold principal incarnation",
		hold({
			hold: 1,
			params: '{"session":"S1"}',
			address: { kind: "session", params: { id: "S1" } },
			principal: { kind: "guest", name: "a" },
			incarnation,
		}),
	],
	["hold incarnation only", hold({ hold: 2, incarnation })],
	["hold chain limit", hold({ hold: 3, chain: [incarnation, other], limit: 65536 })],
	["hold limit 0", hold({ hold: 4, limit: 0 })],
	["hold empty chain", hold({ chain: [] })],
	["hold origin", hold({ origin: 2, type: "t" })],
	["hold deflated params", hold({ params: deflated("000102ff") })],
	["release", { kind: "release", hold: 300 }],
	["release origin", { kind: "release", origin: 2, hold: 3 }],
	["call", { kind: "call", call: 3, hold: 0, method: 1, args: '{"text":"x"}' }],
	["call no args", { kind: "call", call: 4, hold: 0, method: 0 }],
	["call segments", { kind: "call", call: 5, hold: 0, method: 0, args: '{"a":{"$bytes":0},"b":{"$bytes":1}}', segments: ["0102", "03"] }],
	["call deflated", { kind: "call", call: 0, hold: 0, method: 0, args: deflated("000102ff") }],
	["call origin", { kind: "call", origin: 2, call: 1, hold: 3, method: 0, args: "[1]" }],
	["abort", { kind: "abort", call: 3 }],
	["abort origin", { kind: "abort", origin: 300, call: 1 }],
	[
		"bound",
		{
			kind: "bound",
			holds: [{ hold: 0, variant: 1, members: [{ member: 0, value: '[1,"a"]' }], failed: [{ member: 1, error: chord("released", "gone") }] }],
		},
	],
	[
		"bound two",
		{
			kind: "bound",
			holds: [
				{ hold: 0, variant: 0, members: [], failed: [] },
				{ hold: 5, variant: 2, members: [{ member: 1, value: "null" }], failed: [] },
			],
		},
	],
	["bound origin deflated", { kind: "bound", origin: 2, holds: [{ hold: 3, variant: 0, members: [{ member: 0, value: deflated("0102") }], failed: [] }] }],
	[
		"state",
		{
			kind: "state",
			holds: [
				{ hold: 0, members: [{ member: 2, ops: '[["s",["n"],1]]' }], failed: [], retired: [] },
				{ hold: 1, members: [], failed: [{ member: 0, error: chord("released", "gone") }], retired: [] },
				{ hold: 2, members: [{ member: 1, ops: '[["r",{}]]' }], failed: [], retired: [1, 3] },
				{ hold: 3, members: [], failed: [{ member: 0, error: chord("released", "gone") }], retired: [2] },
			],
		},
	],
	["state empty origin", { kind: "state", origin: 2, holds: [] }],
	["state deflated", { kind: "state", holds: [{ hold: 0, members: [{ member: 0, ops: deflated("090807") }], failed: [], retired: [] }] }],
	["failed transient", { kind: "failed", hold: 0, cause: { kind: "transient", error: chord("unavailable", "m") } }],
	["failed broken", { kind: "failed", hold: 0, cause: { kind: "broken", error: chord("unavailable", "m") } }],
	["failed waiting", { kind: "failed", hold: 0, cause: { kind: "waiting", error: chord("contract_mismatch", "m") } }],
	["failed retryIn 0", { kind: "failed", hold: 0, cause: { kind: "transient", error: chord("unavailable", "m") }, retryIn: 0 }],
	["failed retryIn", { kind: "failed", hold: 9, origin: 2, cause: { kind: "transient", error: chord("unavailable", "m") }, retryIn: 60000 }],
	["failed plugin", { kind: "failed", hold: 0, cause: { kind: "broken", error: plugin("config", "m") } }],
	["retry", { kind: "retry", hold: 300 }],
	["retry operation", { kind: "retry", hold: 300, operation: incarnation }],
	["retry through", { kind: "retry", hold: 300, operation: incarnation, through: [incarnation, other] }],
	["retry origin", { kind: "retry", origin: 2, hold: 3 }],
	["loading", { kind: "loading", hold: 300 }],
	["loading origin", { kind: "loading", origin: 2, hold: 3 }],
	["result", { kind: "result", call: 3, value: '{"n":1}' }],
	["result none", { kind: "result", call: 3 }],
	["result origin", { kind: "result", origin: 2, call: 1 }],
	["result bytes part", { kind: "result", call: 0, value: raw('{"n":1}') }],
	["result segments", { kind: "result", call: 0, value: '{"$bytes":0}', segments: ["010203"] }],
	["result deflated segments", { kind: "result", call: 0, value: deflated("0001"), segments: ["07"] }],
	["error plugin", { kind: "error", call: 3, error: plugin("c", "m", [1]) }],
	["error origin", { kind: "error", origin: 2, call: 1, error: plugin("c", "m") }],
	["gone", { kind: "gone", hold: 0, error: chord("released", "m") }],
	["gone plugin details", { kind: "gone", hold: 1, error: plugin("config", "m", { at: 1 }) }],
	["gone origin", { kind: "gone", origin: 2, hold: 3, error: chord("released", "m") }],
	["ack", { kind: "ack" }],
	["ack origin", { kind: "ack", origin: 300 }],
	["closed", { kind: "closed", origin: 300 }],
	["revoke", { kind: "revoke", revocation: { grant: "g1" } }],
	["ping", { kind: "ping" }],
	["pong", { kind: "pong" }],
	["unicode string", hold({ type: "\u{feff}thing é 界 😀", params: json("é界😀\u{feff}") })],
	// Strings around the encoder's thresholds: one-byte length up to 42 units (21 for a part), in place from 4096.
	...[0, 1, 20, 21, 22, 41, 42, 43, 100].flatMap((n) => [
		[`euro type ${n}`, hold({ type: rep("€", n), params: json(rep("€", n)) })],
		[`ascii type ${n}`, hold({ type: rep("x", n), params: json(rep("x", n)) })],
	]),
	...[
		["ASCII", rep("x", 10000)],
		["euro", rep("€", 10000)],
		["surrogate pairs", rep("😀", 5000)],
		["mixed threshold", { $repeat: ["a", 4093] , suffix: "😀€" }],
		["leading feff", { $repeat: ["y", 5000], prefix: "\u{feff}" }],
		["below long", rep("x", 4094)],
		["at long", rep("x", 4095)],
		["past long", rep("x", 4096)],
		["emoji below long", rep("😀", 2047)],
		["emoji at long", rep("😀", 2048)],
	].map(([name, text]) => [`long ${name}`, { kind: "result", call: 0, value: json(text) }]),
	["long bound", { kind: "bound", holds: [{ hold: 0, variant: 0, members: [{ member: 0, value: json(rep("€", 300)) }, { member: 1, value: "1" }], failed: [] }] }],
	["large bound", { kind: "bound", holds: [{ hold: 0, variant: 0, members: [{ member: 0, value: json(rep("x", 500000)) }, { member: 1, value: "1" }], failed: [] }] }],
];

function resolveString(value) {
	if (value !== null && typeof value === "object" && "$repeat" in value) {
		return (value.prefix ?? "") + value.$repeat[0].repeat(value.$repeat[1]) + (value.suffix ?? "");
	}
	return value;
}

const hexBytes = (hex) => Uint8Array.from(hex.match(/../g) ?? [], (pair) => Number.parseInt(pair, 16));

// DeflatedPart is part.ts's class: take it from the copied module.
const { DeflatedPart } = await import(pathToFileURL(join(out, "part.ts")).href);

/** A part from its spec: text, a deflated part, received bytes, or the JSON of a string. */
function toPart(value) {
	if (typeof value === "string") return value;
	if ("$d" in value) return new DeflatedPart(hexBytes(value.$d));
	if ("$b" in value) return new TextEncoder().encode(value.$b);
	if ("$json" in value) return JSON.stringify(resolveString(value.$json));
	throw new Error("unknown part");
}

const PART_KEYS = new Set(["params", "args", "value", "ops"]);

/** A message from its spec: parts built, `$repeat` strings resolved, segments from hex. */
function build(node, key) {
	if (Array.isArray(node)) return node.map((child) => build(child, key));
	if (node !== null && typeof node === "object") {
		if (key !== undefined && PART_KEYS.has(key) && ("$d" in node || "$b" in node || "$json" in node)) return toPart(node);
		if ("$repeat" in node) return resolveString(node);
		return Object.fromEntries(
			Object.entries(node).map(([name, child]) => [name, name === "segments" ? child.map(hexBytes) : build(child, name)]),
		);
	}
	if (typeof node === "string" && key !== undefined && PART_KEYS.has(key)) return node;
	return node;
}

const hex = (bytes) => Buffer.from(bytes).toString("hex");
/** An output as its length and SHA-256, with its hex when short enough to read in a diff. */
const output = (bytes) => ({
	length: bytes.byteLength,
	sha256: createHash("sha256").update(bytes).digest("hex"),
	...(bytes.byteLength <= 1024 ? { hex: hex(bytes) } : {}),
});
const golden = cases.map(([name, spec]) => {
	const built = build(spec, undefined);
	const binary = binaryCodec.encode(built);
	return { name, spec, binary: output(binary), text: output(textCodec.encode(built)) };
});
process.stdout.write(`${JSON.stringify(golden)}\n`);
