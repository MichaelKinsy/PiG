// Reads each file named on stdin through the real packages/durable/src/env/node.ts NodeExecutionEnv.openTextLineReader (pinned upstream mirror) and prints its lines:
// { dir, files: [name] } -> { name: [{ text, terminated }] }.
import { readFileSync } from "node:fs";
const { NodeExecutionEnv } = await import(process.argv[2] + "/.upstream/current/packages/durable/src/env/node.ts");
const { dir, files } = JSON.parse(readFileSync(0, "utf8"));
const env = new NodeExecutionEnv({ cwd: dir });
const context = { abortSignal: new AbortController().signal };
const out = {};
for (const name of files) {
	const opened = await env.openTextLineReader(name, context);
	if (!opened.ok) throw new Error(`${name}: ${opened.error.message}`);
	const reader = opened.value;
	const lines = [];
	while (true) {
		const read = await reader.readLine(context);
		if (!read.ok) throw new Error(`${name}: ${read.error.message}`);
		if (read.value === undefined) break;
		lines.push({ text: read.value.text, terminated: read.value.terminated });
	}
	await reader.close(context);
	out[name] = lines;
}
process.stdout.write(JSON.stringify(out));
