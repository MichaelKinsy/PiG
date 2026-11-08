// Pi's own web client in headless Chromium (Playwright) against a p2 server (ours or Pi's): loads `/`, is the loopback owner,
// completes a faux-provider chat round trip, and reports what the first load cost.
//   node ui.mjs <base url> [server pid]
// Prints one JSON line {firstLoad: {requests, bytes}, rssKiB, ...}; exits non-zero on the first failed expectation.
import { readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

// playwright-core is installed outside the repository (P2_UI_NODE_PATH: the node_modules holding it).
const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");

const [base, pid] = process.argv.slice(2);
if (!base) throw new Error("usage: node ui.mjs <base url> [server pid]");
const rss = (id) => {
	try {
		return Number(/VmRSS:\s+(\d+)/.exec(readFileSync(`/proc/${id}/status`, "utf8"))[1]);
	} catch {
		return 0;
	}
};
// The server's process and every descendant (session workers).
const tree = (root) => {
	const all = new Map();
	for (const entry of readdirSync("/proc").filter((name) => /^\d+$/.test(name))) {
		try {
			all.set(Number(entry), Number(/^\d+ \(.*\) \S (\d+)/.exec(readFileSync(`/proc/${entry}/stat`, "utf8"))[1]));
		} catch {}
	}
	const ids = [root];
	for (let index = 0; index < ids.length; index++) for (const [child, parent] of all) if (parent === ids[index]) ids.push(child);
	return ids;
};

const browser = await chromium.launch({ args: ["--no-sandbox"] });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
const problems = [];
page.on("pageerror", (error) => problems.push(`pageerror: ${error.message}`));
page.on("console", (message) => message.type() === "error" && problems.push(`console: ${message.text()}`));
await page.addInitScript(() => {
	window.cspViolations = [];
	document.addEventListener("securitypolicyviolation", (event) => window.cspViolations.push(`${event.violatedDirective} ${event.blockedURI}`));
});
const cdp = await context.newCDPSession(page);
await cdp.send("Network.enable");
let requests = 0;
let bytes = 0;
const urls = new Map();
cdp.on("Network.requestWillBeSent", (event) => urls.set(event.requestId, event.request.url));
cdp.on("Network.loadingFinished", (event) => {
	requests++;
	bytes += event.encodedDataLength;
});
const expect = (condition, message) => {
	if (!condition) throw new Error(message);
};

let firstLoad;
let reply;
try {
const response = await page.goto(`${base}/`, { waitUntil: "load" });
expect(response?.status() === 200, `GET / answered ${response?.status()}`);
await page.waitForSelector(".composer textarea", { timeout: 60000 });
expect((await page.$(".offline")) === null, "the page shows offline");
await page.waitForLoadState("networkidle");
// The new-session screen is ready once it has a directory and a model (the fake provider's).
await page.waitForFunction(() => document.querySelector(".cwd-pick") && document.querySelector(".model-control")?.textContent !== "Choose model", null, { timeout: 60000 });
firstLoad = { requests, bytes };
expect((await page.evaluate("window.cspViolations")).length === 0, `CSP violations: ${await page.evaluate("window.cspViolations")}`);
// The loopback owner: the page's cookie is set by the server, no sign-in page.
const cookies = await context.cookies(base);
expect(cookies.length > 0, "the loopback owner cookie was not set");
expect((await page.title()) === "Pi", `title ${await page.title()}`);

// A faux-provider chat round trip: the fake provider echoes the prompt.
const text = "hello parity";
await page.fill(".composer textarea", text);
await page.click("button[aria-label=Send]");
await page.waitForFunction((want) => document.querySelector(".messages .assistant")?.textContent.includes(want), `Echo: ${text}`, { timeout: 60000 });
reply = await page.textContent(".messages .assistant");
// The session page survives a reload (its URL names the session).
expect(/^\/s\/[0-9a-f-]+/.test(new URL(page.url()).pathname), `session URL ${page.url()}`);
await page.reload({ waitUntil: "load" });
await page.waitForFunction((want) => document.querySelector(".messages .assistant")?.textContent.includes(want), `Echo: ${text}`, { timeout: 60000 });
} catch (error) {
	console.error(`FAILED: ${error.message}\nproblems: ${JSON.stringify(problems)}\npage: ${page.url()}\n${(await page.evaluate("document.body.innerText")).slice(0, 2000)}`);
	await page.screenshot({ path: process.env.P2_UI_SHOT ?? "/tmp/ui-failure.png" }).catch(() => {});
	process.exit(1);
}
await browser.close();

const ids = pid ? tree(Number(pid)) : [];
const manifest = await (await fetch(`${base}/__p2_web`)).json();
console.log(JSON.stringify({ base, manifest, firstLoad, reply: reply.trim(), problems, serverRssKiB: pid ? rss(pid) : null, treeRssKiB: ids.reduce((sum, id) => sum + rss(id), 0), processes: ids.length }));
expect(problems.length === 0, `browser problems: ${problems.join("; ")}`);
