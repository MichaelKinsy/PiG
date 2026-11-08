// Ports the last case of test/p2/web.test.ts: the app runs under its CSP (no violation), and another origin cannot frame it
// (Chromium refuses to render the framed page: X-Frame-Options DENY and CSP frame-ancestors 'none').
//   node frame.mjs <base url>
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const base = process.argv[2];
if (!base) throw new Error("usage: node frame.mjs <base url>");
const framer = createServer((_request, response) => {
	response.writeHead(200, { "Content-Type": "text/html" });
	response.end(`<iframe id="app" src="${base}/"></iframe>`);
});
await new Promise((resolve) => framer.listen(0, "127.0.0.1", resolve));
const browser = await chromium.launch({ args: ["--no-sandbox"] });
const fail = async (message) => {
	console.error(`FAILED: ${message}`);
	await browser.close();
	framer.close();
	process.exit(1);
};
const page = await (await browser.newContext()).newPage();
await page.addInitScript(() => {
	window.cspViolations = [];
	document.addEventListener("securitypolicyviolation", (event) => window.cspViolations.push(`${event.violatedDirective} ${event.blockedURI}`));
});
await page.goto(`${base}/`);
await page.waitForSelector(".composer textarea", { timeout: 60000 });
await page.waitForLoadState("networkidle");
const violations = await page.evaluate("window.cspViolations");
if (violations.length) await fail(`CSP violations: ${violations}`);
// A page of another origin (another port, same site) framing the app: Chromium refuses to render it.
await page.goto(`http://127.0.0.1:${framer.address().port}/`);
await new Promise((resolve) => setTimeout(resolve, 1500));
const cdp = await page.context().newCDPSession(page);
const { frameTree } = await cdp.send("Page.getFrameTree");
const child = frameTree.childFrames?.[0]?.frame;
if (!child) await fail("the framer has no iframe");
if (!(child.url.startsWith("chrome-error:") || child.unreachableUrl !== undefined)) await fail(`the app rendered in a frame: ${JSON.stringify(child)}`);
console.log(JSON.stringify({ framed: "refused", child: child.url }));
await browser.close();
framer.close();
