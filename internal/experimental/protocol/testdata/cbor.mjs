// Run Pi's packages/protocol/src/cbor encoder and decoder over probes from stdin.
// A probe is {op: "decode", hex, opts} or {op: "encode", spec, opts}; the result is {ok} (a rendering for decode, hex for encode) or {err: "Name: message"}.
// usage: node cbor.mjs <path to packages/protocol/src/cbor/index.ts>
import { pathToFileURL } from "node:url";
const { decodeCbor, encodeCbor } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const hexOf = (bytes) => Buffer.from(bytes).toString("hex");
const f64 = new Float64Array(1);
const u64 = new BigUint64Array(f64.buffer);
const bits = (n) => { f64[0] = n; return u64[0].toString(16).padStart(16, "0"); };
const fromBits = (h) => { u64[0] = BigInt("0x" + h); return f64[0]; };
const textHex = (s) => hexOf(Buffer.from(s, "utf8"));
function render(v) {
	if (v === undefined) return "u";
	if (v === null) return "null";
	if (typeof v === "boolean") return String(v);
	if (typeof v === "number") return "n" + bits(v);
	if (typeof v === "string") return "s" + textHex(v);
	if (v instanceof Uint8Array) return "b" + hexOf(v);
	if (Array.isArray(v)) return "[" + v.map(render).join(",") + "]";
	return "{" + Object.keys(v).map((k) => "s" + textHex(k) + ":" + render(v[k])).join(",") + "}";
}
function build(spec) {
	switch (spec.k) {
		case "null": return null;
		case "undef": return undefined;
		case "bool": return spec.v;
		case "num": return fromBits(spec.bits);
		case "str": return spec.bad ? "\ud800" : Buffer.from(spec.hex, "hex").toString("utf8");
		case "bytes": return new Uint8Array(Buffer.from(spec.hex, "hex"));
		case "arr": return spec.items.map(build);
		case "obj": {
			const o = {};
			for (const [key, value] of spec.props) {
				Object.defineProperty(o, Buffer.from(key, "hex").toString("utf8"), { value: build(value), enumerable: true, configurable: true, writable: true });
			}
			return o;
		}
	}
	throw new Error("bad spec " + spec.k);
}
const options = (o) => (o === null ? undefined : Object.fromEntries(Object.entries(o).filter(([, v]) => v !== null)));
const results = JSON.parse(input).map((probe) => {
	try {
		if (probe.op === "decode") return { ok: render(decodeCbor(new Uint8Array(Buffer.from(probe.hex, "hex")), options(probe.opts))) };
		return { ok: hexOf(encodeCbor(build(probe.spec), options(probe.opts))) };
	} catch (error) {
		return { err: `${error.name}: ${error.message}` };
	}
});
process.stdout.write(JSON.stringify(results));
