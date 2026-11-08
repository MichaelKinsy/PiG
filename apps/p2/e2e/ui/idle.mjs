// CPU of a p2 server with a UI client attached and idle: the page is loaded, a chat round trip is done (so a session
// exists and its worker runs), then nothing happens for the sampling window; the server process and its descendants are
// sampled from /proc.   node idle.mjs <base url> <server pid> [seconds]
import { readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const [base, pid, seconds = "30"] = process.argv.slice(2);
const ticksPerSecond = 100;
const stat = (id) => {
	const text = readFileSync(`/proc/${id}/stat`, "utf8");
	const fields = text.slice(text.lastIndexOf(")") + 2).split(" ");
	return { parent: Number(fields[1]), ticks: Number(fields[11]) + Number(fields[12]) };
};
const tree = (root) => {
	const parents = new Map();
	for (const entry of readdirSync("/proc").filter((name) => /^\d+$/.test(name))) {
		try {
			parents.set(Number(entry), stat(entry).parent);
		} catch {}
	}
	const ids = [root];
	for (let index = 0; index < ids.length; index++) for (const [child, parent] of parents) if (parent === ids[index]) ids.push(child);
	return ids;
};
const sample = () => Object.fromEntries(tree(Number(pid)).map((id) => [id, stat(id).ticks]));
const browser = await chromium.launch({ args: ["--no-sandbox"] });
const page = await (await browser.newContext({ viewport: { width: 1280, height: 820 } })).newPage();
await page.goto(`${base}/`);
await page.waitForFunction(() => document.querySelector(".cwd-pick") && document.querySelector(".model-control")?.textContent !== "Choose model", null, { timeout: 60000 });
// SESSIONS (default 1) sessions, each with one round trip, the last one left open.
for (let index = 1; index <= Number(process.env.SESSIONS ?? 1); index++) {
	if (index > 1) await page.goto(`${base}/`);
	await page.waitForFunction(() => document.querySelector(".cwd-pick") && document.querySelector(".model-control")?.textContent !== "Choose model", null, { timeout: 60000 });
	await page.fill(".composer textarea", `hello idle ${index}`);
	await page.click("button[aria-label=Send]");
	await page.waitForFunction((want) => document.querySelector(".messages .assistant")?.textContent.includes(want), `Echo: hello idle ${index}`, { timeout: 60000 });
}
await new Promise((resolve) => setTimeout(resolve, 5000));
const before = sample();
const started = Date.now();
await new Promise((resolve) => setTimeout(resolve, Number(seconds) * 1000));
const after = sample();
const elapsed = (Date.now() - started) / 1000;
const rows = Object.keys(after).map((id) => {
	let command = "";
	try {
		command = readFileSync(`/proc/${id}/cmdline`, "utf8").split("\0").slice(0, 3).join(" ").slice(0, 60);
	} catch {}
	return { id, command, cpuPercent: Number((((after[id] - (before[id] ?? 0)) / ticksPerSecond / elapsed) * 100).toFixed(2)) };
});
console.log(JSON.stringify({ seconds: elapsed, rows, serverPercent: rows[0].cpuPercent, totalPercent: Number(rows.reduce((sum, row) => sum + row.cpuPercent, 0).toFixed(2)) }));
await browser.close();
