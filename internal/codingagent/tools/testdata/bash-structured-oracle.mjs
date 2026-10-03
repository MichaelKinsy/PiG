// Records the real upstream 0.99.1 bash tool result for a fixed command set.
// Run in a directory with @earendil-works/pi-coding-agent@0.99.1 installed:
//   node bash-structured-oracle.mjs > bash-structured-oracle.json
// The recorded values come from upstream, never from Go output.
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { createBashTool } from "@earendil-works/pi-coding-agent";

const version = JSON.parse(readFileSync(new URL("./node_modules/@earendil-works/pi-coding-agent/package.json", import.meta.url), "utf8")).version;
if (version !== "1.0.0") throw new Error(`oracle needs upstream 1.0.0, found ${version}`);

const sha = (text) => createHash("sha256").update(text).digest("hex");
const commands = {
	fail: "echo out; exit 3",
	empty: "true",
	seq3000: "seq 1 3000",
	seq300000: "seq 1 300000",
	signal: "printf 'before-kill\\n'; kill -KILL $$",
	// 3-byte characters: the 512 KiB head ends inside one and the tail starts on a continuation byte.
	euro2m: "yes '€' | tr -d '\\n' | head -c 2000001",
};
const tool = createBashTool(process.cwd());
const cases = {};
for (const [name, command] of Object.entries(commands)) {
	const result = await tool.execute(`oracle-${name}`, { command });
	const structured = result.structuredContent;
	const text = result.content.map((block) => block.text).join("\n");
	const details = result.details;
	cases[name] = {
		command,
		isError: result.isError === true,
		detailsKeys: details === undefined ? null : Object.keys(details),
		textBytes: Buffer.byteLength(text),
		textSha256: sha(text.replace(details?.fullOutputPath ?? "\0", "<path>")),
		structuredKeys: Object.keys(structured),
		truncated: structured.truncated,
		exitCode: structured.exit_code,
		hasFullOutputPath: structured.full_output_path !== undefined,
		fullOutputPathIsDetailsPath: structured.full_output_path === details?.fullOutputPath,
		outputBytes: Buffer.byteLength(structured.output),
		outputSha256: sha(structured.output),
		outputHead: structured.output.slice(0, 16),
		outputTail: structured.output.slice(-16),
	};
}
console.log(JSON.stringify({ upstream: version, cases }, null, "\t"));
