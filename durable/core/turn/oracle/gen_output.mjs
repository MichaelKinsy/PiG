// Oracle for bounded tool output (src/harness/output.ts). Lines:
//   B <hex text> <maxBytes> <maxLines> <retain> <hex JSON boundOutput>
//   O <hex JSON {limits, ops}> <hex JSON snapshots>   ops: {k:"s",v}, {k:"b",v:<hex>,skip?}, {k:"snap"}; the last snapshot follows end()
import { writeFileSync } from "node:fs";
import { PI } from "./pimain.mjs";
import { rng, hex } from "./rng.mjs";

const o = await import(`${PI}/packages/durable/src/harness/output.ts`);
const r = rng(Number(process.argv[2] ?? 4));
const out = process.argv[3] ?? new URL("../testdata/output.hex", import.meta.url).pathname;
const lines = [];
const J = (v) => JSON.stringify(v);

const atoms = ["a", "bc", "\n", "\n\n", "é", "😀", "\t", "\u0001", "x".repeat(20), "\ufeff", "\ufffa", "日本語", " ", "\r\n", "z".repeat(3), "日本語".repeat(6), "😀é".repeat(5)];
const text = (n) => Array.from({ length: n }, () => r.pick(atoms)).join("");

for (let i = 0; i < 500; i++) {
	const t = text(r.int(14));
	const limits = { maxBytes: r.pick([0, 1, 2, 3, 5, 8, 20, 100]), maxLines: r.pick([0, 1, 2, 3, 5, 50]), retain: r.pick(["head", "tail"]) };
	lines.push(`B ${hex(t)} ${limits.maxBytes} ${limits.maxLines} ${limits.retain} ${hex(J(o.boundOutput(t, limits)))}`);
}
const enc = new TextEncoder();
for (let i = 0; i < 400; i++) {
	const limits = { maxBytes: r.pick([3, 8, 20, 60]), maxLines: r.pick([1, 2, 4, 10]), retain: r.pick(["head", "tail"]) };
	const buf = new o.OutputBuffer(limits);
	const ops = [];
	const snaps = [];
	const n = 1 + r.int(12);
	for (let k = 0; k < n; k++) {
		const c = r.int(10);
		if (c === 0) { ops.push({ k: "snap" }); snaps.push(buf.snapshot()); continue; }
		const skip = limits.retain === "tail" && r.chance(0.2) ? { bytes: r.int(50), newlines: r.int(5), endsWithNewline: r.chance(0.5) } : undefined;
		if (c < 5) {
			const v = text(r.int(6));
			ops.push({ k: "s", v, ...(skip ? { skip } : {}) });
			buf.push(v, skip);
		} else {
			let bytes = [...enc.encode(text(r.int(6)))];
			if (r.chance(0.3) && bytes.length > 1) bytes = bytes.slice(0, 1 + r.int(bytes.length - 1)); // split a character
			if (r.chance(0.25)) bytes.push(...r.pick([[0x80], [0xc3], [0xe2], [0xf0], [0xff], [0xed, 0xa0], [0xe0, 0x80], [0xf4, 0x90], [0xf0, 0x80], [0xe2, 0x82], [0xed, 0xa0, 0x80]]));
			const v = Buffer.from(bytes).toString("hex");
			ops.push({ k: "b", v, ...(skip ? { skip } : {}) });
			buf.push(new Uint8Array(bytes), skip);
		}
	}
	buf.end();
	ops.push({ k: "end" });
	snaps.push(buf.snapshot());
	lines.push(`O ${hex(J({ limits, ops }))} ${hex(J(snaps))}`);
}
writeFileSync(out, lines.join("\n") + "\n");
console.log(lines.length, "lines");
