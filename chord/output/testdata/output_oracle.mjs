// Run Pi's output bounding (packages/chord/src/output.ts, imported as-is with types stripped) over programs from stdin.
// A program is {limits: {maxBytes, maxLines, retain}, ops}: {op: "push", text} | {op: "bytes", data (base64)} | {op: "skip", text | data, skipped: {bytes, newlines, endsWithNewline}} | {op: "end"} | {op: "snapshot"} | {op: "bound", text} | {op: "sanitize", text}.
// stdout: per program, one {ok, stored} or {err} per op: ok is the push result, the snapshot or the bound slice; stored is storedBytes after the op.
// usage: node output_oracle.mjs <packages dir>
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const { OutputBuffer, boundOutput, sanitizeOutput } = await import(pathToFileURL(`${packages}chord/src/output.ts`).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const program of JSON.parse(input)) {
	const buffer = new OutputBuffer(program.limits);
	const out = [];
	for (const op of program.ops) {
		try {
			let ok;
			if (op.op === "push") ok = buffer.push(op.text);
			else if (op.op === "bytes") ok = buffer.push(new Uint8Array(Buffer.from(op.data, "base64")));
			else if (op.op === "skip") ok = buffer.push(op.data !== undefined ? new Uint8Array(Buffer.from(op.data, "base64")) : op.text, op.skipped);
			else if (op.op === "end") (buffer.end(), (ok = null));
			else if (op.op === "snapshot") ok = buffer.snapshot();
			else if (op.op === "bound") ok = boundOutput(op.text, program.limits);
			else if (op.op === "sanitize") ok = sanitizeOutput(op.text);
			out.push({ ok, stored: buffer.storedBytes });
		} catch (error) {
			out.push({ err: error?.message ?? String(error), stored: buffer.storedBytes });
		}
	}
	results.push(out);
}
process.stdout.write(JSON.stringify(results));
