// The PiG mark (the sidebar's, the favicon and the empty state's head beside its heading) follows the selected sprite in live pages (light and dark desktop, light phone): the
// default sprite first, then each sprite the test selects (one stdin line per step, the sprite's name; "end" finishes), with
// a screenshot of the empty state of every page at every step.
//   node sprite.mjs <base url> <output directory>
import { mkdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { createInterface } from "node:readline";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const [base, out] = process.argv.slice(2);
if (!base || !out) throw new Error("usage: node sprite.mjs <base url> <output directory>");
mkdirSync(out, { recursive: true });
const lines = createInterface({ input: process.stdin });
const queue = [];
let waiting;
lines.on("line", (line) => (waiting ? waiting(line) : queue.push(line)));
const nextLine = () => (queue.length ? Promise.resolve(queue.shift()) : new Promise((resolve) => (waiting = resolve)));
const fail = (message) => {
	console.error(`FAILED: ${message}`);
	process.exit(1);
};
const browser = await chromium.launch({ args: ["--no-sandbox"] });
const views = [
	{ name: "light", theme: "light", viewport: { width: 1280, height: 820 } },
	{ name: "dark", theme: "dark", viewport: { width: 1280, height: 820 } },
	{ name: "phone", theme: "light", viewport: { width: 390, height: 800 } },
];
for (const view of views) {
	const context = await browser.newContext({ viewport: view.viewport });
	await context.addInitScript((value) => localStorage.setItem("pi-theme", value), view.theme);
	view.page = await context.newPage();
	await view.page.goto(`${base}/`);
	await view.page.waitForSelector(".hero-title .pi-logo", { timeout: 60000 });
}
const state = (page) =>
	page.evaluate(() => {
		const mark = document.querySelector(".pi-logo");
		const icon = document.querySelector('link[rel="icon"]');
		const info = JSON.parse(document.documentElement.dataset.brandSprite || "null");
		const head = document.querySelector(".hero-title .pi-logo");
		const box = head?.getBoundingClientRect();
		const title = document.querySelector(".hero h2")?.getBoundingClientRect();
		return {
			key: info?.key,
			name: info?.name,
			headSrc: head?.getAttribute("src"),
			headLoaded: head instanceof HTMLImageElement && head.complete && head.naturalWidth > 0,
			headScale: box ? box.width / 16 : 0,
			headBeside: box && title ? box.right <= title.left && Math.abs(box.top + box.height / 2 - (title.top + title.height / 2)) < 2 : false,
			extras: document.querySelectorAll(".brand-banner, .brand-tagline").length,
			mark: mark?.tagName,
			markSrc: mark?.getAttribute("src"),
			icon: icon?.getAttribute("href"),
			heading: document.querySelector(".hero h2")?.textContent,
		};
	});
const check = (label, current, theme) => {
	const problems = [];
	if (!current.key) problems.push("no sprite info");
	if (current.mark !== "IMG" || !current.markSrc?.includes(`/__p2_brand/sprite.svg?v=${current.key}`)) problems.push("the mark is not the sprite");
	if (!current.icon?.includes(`/__p2_brand/sprite.svg?v=${current.key}`)) problems.push("the favicon is not the sprite");
	if (!current.headSrc?.includes(`/__p2_brand/sprite.svg?v=${current.key}`) || !current.headLoaded) problems.push("the empty state's head is not the sprite");
	if (!current.headBeside) problems.push("the head is not inline left of the heading");
	if (current.extras) problems.push("the empty state shows a banner or tagline");
	if (current.heading !== "What should PiG do?") problems.push(`heading ${current.heading}`);
	if (!Number.isInteger(current.headScale)) problems.push(`the head is at ${current.headScale}x, not a whole multiple`);
	if (problems.length) fail(`${label}: ${problems.join("; ")} ${JSON.stringify(current)}`);
};
let previous;
let step = 0;
for (;;) {
	let label = "default";
	if (step > 0) {
		console.log(`STEP ${step}`);
		label = await nextLine();
		if (label === "end") break;
		await Promise.all(views.map(({ page }) => page.waitForFunction((was) => JSON.parse(document.documentElement.dataset.brandSprite || "null")?.key !== was && document.querySelector(".hero-title .pi-logo")?.complete, previous, { timeout: 30000 })));
	}
	await Promise.all(views.map(({ page }) => page.waitForTimeout(300)));
	let shown;
	for (const view of views) {
		const current = await state(view.page);
		check(`${label}/${view.name}`, current, view.theme);
		shown ??= current;
		await view.page.screenshot({ path: join(out, `${label}-${view.name}.png`) });
	}
	previous = shown.key;
	console.log(JSON.stringify({ label, key: shown.key, name: shown.name, scale: shown.headScale }));
	step++;
}
console.log("DONE");
await browser.close();
process.exit(0);
