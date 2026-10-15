// Run Pi's packages/durable/src/truncate.ts (imported as-is with types stripped) over ops from stdin.
// Op: {op: "head", content, maxLines?, maxBytes?} | {op: "headOf", prefix, lines, bytes, maxLines?, maxBytes?} | {op: "size", bytes} | {op: "bytes", content}
// stdout: per op {ok} or {err}.
// usage: node truncate_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { truncateHead, truncateHeadOf, formatSize, utf8ByteLength } = await import(pathToFileURL(`${packages}durable/src/truncate.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	const options = {};
	if (op.maxLines !== undefined && op.maxLines !== null) options.maxLines = op.maxLines;
	if (op.maxBytes !== undefined && op.maxBytes !== null) options.maxBytes = op.maxBytes;
	try {
		if (op.op === "head") out.push({ ok: truncateHead(op.content, options) });
		else if (op.op === "headOf") out.push({ ok: truncateHeadOf(op.prefix, { lines: op.lines, bytes: op.bytes }, options) });
		else if (op.op === "size") out.push({ ok: formatSize(op.bytes) });
		else out.push({ ok: utf8ByteLength(op.content) });
	} catch (error) {
		out.push({ err: String(error) });
	}
}
process.stdout.write(JSON.stringify(out));
