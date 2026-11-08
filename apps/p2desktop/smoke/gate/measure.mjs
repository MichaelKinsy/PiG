// The web client's half of the long-session measurement (native half: TestMeasureLong): open the session, scroll back through
// its history in wheel steps, then stream one answer; frame times are from the wheel (or the key) to the next painted frame.
//   P2_UI_NODE_PATH=<node_modules with playwright-core> node measure.mjs <url of /s/<id>> <exchanges> <out.json>
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const [url, exchanges, out] = process.argv.slice(2);
const profile = mkdtempSync(join(tmpdir(), "pd3-web-"));
const context = await chromium.launchPersistentContext(profile, {
	args: ["--no-sandbox"],
	viewport: { width: 1280, height: 812 },
	...(process.env.P2_CHROME_BIN ? { executablePath: process.env.P2_CHROME_BIN } : {}),
});
await context.addInitScript(() => localStorage.setItem("pi-theme", "dark"));
const page = context.pages()[0] ?? (await context.newPage());
const stats = (values) => {
	const sorted = [...values].sort((a, b) => a - b);
	const sum = values.reduce((a, b) => a + b, 0);
	const r = (x) => Math.round(x * 100) / 100;
	return { n: values.length, mean: r(sum / values.length), p95: r(sorted[Math.floor(sorted.length * 0.95)]), max: r(sorted.at(-1)) };
};
const nextFrame = () => page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
const figures = { exchanges: Number(exchanges) };
const opened = Date.now();
await page.goto(url);
await page.getByText(`message ${Number(exchanges) - 1}: please answer`).first().waitFor({ timeout: 120000 });
figures.openMs = Date.now() - opened;
const scroller = page.locator(".scroller").first();
const box = await scroller.boundingBox();
await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
const frames = [];
const started = Date.now();
for (let i = 0; i < 400; i++) {
	const t0 = performance.now();
	await page.mouse.wheel(0, -400);
	await nextFrame();
	frames.push(performance.now() - t0);
}
figures.scrollUp400Ms = Date.now() - started;
figures.oldestVisible = await page.evaluate(() => {
	// the oldest numbered message whose box is on screen (the page may keep every message in the DOM)
	let oldest = -1;
	for (const el of document.querySelectorAll(".scroller p, .scroller div, .scroller span")) {
		if (el.children.length > 0) continue;
		const m = /message (\d+): please answer/.exec(el.textContent ?? "");
		if (!m) continue;
		const r = el.getBoundingClientRect();
		if (r.bottom < 0 || r.top > innerHeight) continue;
		if (oldest < 0 || Number(m[1]) < oldest) oldest = Number(m[1]);
	}
	return oldest;
});
figures.scrollFrameMs = stats(frames);
await page.keyboard.press("End");
await page.getByPlaceholder("Message PiG…").fill("stream one more answer");
await page.keyboard.press("Enter");
const run = [];
const end = Date.now() + 20000;
let last = performance.now();
while (Date.now() < end) {
	await nextFrame();
	const now = performance.now();
	run.push(now - last);
	last = now;
	if ((await page.getByText("Working").count()) === 0 && run.length > 5) break;
}
figures.streamFrameMs = stats(run);
const client = await context.newCDPSession(page);
const metrics = Object.fromEntries((await client.send("Performance.getMetrics")).metrics.map((m) => [m.name, m.value]));
figures.jsHeapMB = Math.round(metrics.JSHeapUsedSize / 1048576);
const ps = execFileSync("ps", ["-axo", "rss=,command="], { encoding: "utf8" });
figures.browserRssMB = Math.round(
	ps.split("\n").filter((l) => l.includes(profile)).reduce((sum, l) => sum + Number(l.trim().split(/\s+/)[0]), 0) / 1024,
);
writeFileSync(out, JSON.stringify(figures, null, 2));
await context.close();
