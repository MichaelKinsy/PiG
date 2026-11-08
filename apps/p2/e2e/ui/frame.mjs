// Ports the last case of test/p2/web.test.ts: the app runs under its CSP (no violation), and another origin cannot frame it
// (Chromium refuses to render the framed page: X-Frame-Options DENY and CSP frame-ancestors 'none').
//   node frame.mjs <base url>
import { createServer, request as httpRequest } from "node:http";
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
// The same frame, but the request reaches the server without Fetch Metadata (through a forwarding proxy, as a script's would),
// so the server serves the owner's app page: the refusal then comes from that page's own X-Frame-Options and CSP
// frame-ancestors, not from the sign-in page's (a browser marks every frame request, which the server reads as signed out).
const basePort = new URL(base).port;
const served = [];
const proxy = createServer((request, response) => {
	const upstream = httpRequest({ host: "127.0.0.1", port: basePort, path: request.url, method: request.method, headers: { host: `127.0.0.1:${basePort}` } }, (reply) => {
		served.push(`${reply.statusCode} ${request.url}`);
		response.writeHead(reply.statusCode, reply.headers);
		reply.pipe(response);
	});
	upstream.on("error", () => response.destroy());
	upstream.end();
});
await new Promise((resolve) => proxy.listen(0, "127.0.0.1", resolve));
const probeFramer = createServer((_request, response) => {
	response.writeHead(200, { "Content-Type": "text/html" });
	response.end(`<iframe id="app" src="http://127.0.0.1:${proxy.address().port}/"></iframe>`);
});
await new Promise((resolve) => probeFramer.listen(0, "127.0.0.1", resolve));
const probe = await (await browser.newContext()).newPage();
await probe.goto(`http://127.0.0.1:${probeFramer.address().port}/`);
await new Promise((resolve) => setTimeout(resolve, 2000));
const probeTree = await (await probe.context().newCDPSession(probe)).send("Page.getFrameTree");
const probed = probeTree.frameTree.childFrames?.[0]?.frame;
if (!probed) await fail("the probe framer has no iframe");
if (!served.includes("200 /")) await fail(`the framed request was not served as the owner's page: ${served}`);
if (!(probed.url.startsWith("chrome-error:") || probed.unreachableUrl !== undefined)) await fail(`the owner's app page rendered in a frame: ${JSON.stringify(probed)}`);
proxy.close();
probeFramer.close();
console.log(JSON.stringify({ framed: "refused", child: child.url, authenticatedChild: probed.url, served }));
await browser.close();
framer.close();
