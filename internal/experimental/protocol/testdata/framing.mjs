// Run Pi's packages/protocol/src/framing.ts over probes from stdin: [{max, ops}] -> [{ctor, steps}].
// max is the maxFrameLength option (omitted when null; a number is passed as is); an op is {push: hex}, {end: true} or {encode: hex}.
// usage: node framing.mjs <path to packages/protocol/src/framing.ts>
import { pathToFileURL } from "node:url";
const { FrameDecoder, encodeFrame } = await import(pathToFileURL(process.argv[2]).href);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const hex = (bytes) => Buffer.from(bytes).toString("hex");
const unhex = (text) => new Uint8Array(Buffer.from(text, "hex"));
const failure = (error) => `${error.name}: ${error.message}`;
const results = JSON.parse(input).map((probe) => {
	let decoder;
	try {
		decoder = new FrameDecoder(probe.max === null ? undefined : { maxFrameLength: probe.max });
	} catch (error) {
		return { ctor: failure(error), steps: [] };
	}
	const steps = (probe.ops ?? []).map((op) => {
		try {
			if (op.push !== undefined) return { frames: decoder.push(unhex(op.push)).map(hex) };
			if (op.encode !== undefined) return { frames: [hex(encodeFrame(unhex(op.encode)))] };
			decoder.end();
			return { frames: [] };
		} catch (error) {
			return { error: failure(error) };
		}
	});
	return { ctor: "", steps };
});
process.stdout.write(JSON.stringify(results));
