// Pages directories through the real packages/durable/src/env/node.ts NodeExecutionEnv.openDirReader (pinned upstream mirror):
// { dir, paths: [relative directory], sizes: [maxEntries], bad: [maxEntries that next must refuse], open: [paths openDirReader must refuse] }
// -> { listings: { "path size": { pages: [{ n, done }], entries: [{ name, path, kind, size }] } }, bad: { size: code }, open: { path: code } }.
import { readFileSync } from "node:fs";
const { NodeExecutionEnv } = await import(process.argv[2] + "/.upstream/current/packages/durable/src/env/node.ts");
const { dir, paths, sizes, bad, open } = JSON.parse(readFileSync(0, "utf8"));
const env = new NodeExecutionEnv({ cwd: dir });
const context = { abortSignal: new AbortController().signal };
const out = { listings: {}, bad: {}, open: {} };
for (const path of paths) {
	for (const size of sizes) {
		const opened = await env.openDirReader(path, context);
		if (!opened.ok) throw new Error(`${path}: ${opened.error.message}`);
		const reader = opened.value;
		const pages = [];
		const entries = [];
		while (true) {
			const next = await reader.next(size, context);
			if (!next.ok) throw new Error(`${path}: ${next.error.message}`);
			pages.push({ n: next.value.entries.length, done: next.value.done });
			for (const e of next.value.entries) entries.push({ name: e.name, path: e.path, kind: e.kind, size: e.size });
			if (next.value.done) break;
		}
		const after = await reader.next(size, context);
		pages.push({ n: after.ok ? after.value.entries.length : -1, done: after.ok ? after.value.done : false });
		await reader.close(context);
		entries.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
		out.listings[`${path} ${size}`] = { pages, entries };
	}
}
for (const size of bad) {
	const opened = await env.openDirReader(paths[0], context);
	const next = await opened.value.next(size, context);
	out.bad[String(size)] = next.ok ? "ok" : next.error.code;
	await opened.value.close(context);
}
for (const path of open) {
	const opened = await env.openDirReader(path, context);
	out.open[path] = opened.ok ? "ok" : opened.error.code;
}
process.stdout.write(JSON.stringify(out));
