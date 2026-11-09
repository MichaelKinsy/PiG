// Review addition (RevP2Next): the Chromium case of test/p2/foreign-requests.test.ts (Pi 7d073328) that the port did not carry.
// A page on another local port (same site, other origin) can neither read nor drive p2 with the owner's cookie, which SameSite
// does not keep from such requests; its link opens the app for the owner's browser, and a browser without a p2 cookie gets
// the sign-in page (loaded once more from p2's origin).
//   node foreign.mjs <base url>      (P2_UI_NODE_PATH: node_modules with playwright-core; P2_UI_COOKIE: `name=value`)
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const p2 = process.argv[2];
const pair = process.env.P2_UI_COOKIE;
if (!p2 || !pair) throw new Error("usage: P2_UI_COOKIE=name=value node foreign.mjs <base url>");
const [cookieName, ...cookieValue] = pair.split("=");
const page = `<a id="open" href="${p2}/">open</a>
<script type="module">
const p2 = ${JSON.stringify(p2)};
const results = {};
const attempt = async (name, fn) => {
	try { await fn(); results[name] = "read"; } catch { results[name] = "blocked"; }
};
await attempt("check", async () => (await fetch(p2 + "/owner/check", { credentials: "include" })).text());
await attempt("token", async () => (await fetch(p2 + "/owner-token", { credentials: "include" })).text());
await attempt("claimJson", async () => (await fetch(p2 + "/owner/local/challenge", { method: "POST", credentials: "include", headers: { "Content-Type": "application/json" }, body: "{}" })).text());
// A no-cors request is sent and its answer hidden: the server's status is what counts (below).
await fetch(p2 + "/owner/logout-all", { method: "POST", credentials: "include", mode: "no-cors" }).catch(() => {});
await fetch(p2 + "/owner/local/challenge", { method: "POST", credentials: "include", mode: "no-cors", body: "{}" }).catch(() => {});
results.socket = await new Promise((resolve) => {
	const socket = new WebSocket(p2.replace("http", "ws") + "/chord");
	socket.onopen = () => resolve("open");
	socket.onerror = () => resolve("refused");
});
window.results = results;
</script>`;
const foreign = createServer((_request, response) => {
	response.writeHead(200, { "Content-Type": "text/html" });
	response.end(page);
});
await new Promise((resolve) => foreign.listen(0, "127.0.0.1", resolve));
const foreignOrigin = `http://127.0.0.1:${foreign.address().port}`;
const browser = await chromium.launch({ args: ["--no-sandbox"], executablePath: process.env.P2_UI_CHROMIUM || undefined });
let failed;
const fail = (message) => { failed ??= message; };
try {
	const owner = await browser.newContext();
	await owner.addCookies([{ name: cookieName, value: cookieValue.join("="), url: p2, httpOnly: true, sameSite: "Lax" }]);
	const ownerPage = await owner.newPage();
	await ownerPage.goto(`${p2}/`);
	await ownerPage.waitForSelector(".composer textarea", { timeout: 60000 });
	// Every request the browser sends to p2 (CDP, as Pi's test does): its URL, whether it carried the owner cookie, p2's status.
	const cdp = await owner.newCDPSession(ownerPage);
	const byId = new Map();
	const entry = (id) => { if (!byId.has(id)) byId.set(id, { url: "" }); return byId.get(id); };
	cdp.on("Network.requestWillBeSent", ({ requestId, request }) => { const e = entry(requestId); e.url = request.url; e.method = request.method; });
	cdp.on("Network.requestWillBeSentExtraInfo", ({ requestId, headers }) => {
		const e = entry(requestId);
		for (const [name, value] of Object.entries(headers)) {
			if (name.toLowerCase() === "cookie") e.cookie = String(value).includes(`${cookieName}=`);
			if (name.toLowerCase() === "sec-fetch-site") e.site = value;
		}
	});
	cdp.on("Network.responseReceivedExtraInfo", ({ requestId, statusCode }) => { entry(requestId).status = statusCode; });
	await cdp.send("Network.enable");
	const seen = { get length() { return byId.size; }, filter: (f) => [...byId.values()].filter(f) };
	await ownerPage.goto(`${foreignOrigin}/`);
	await ownerPage.waitForFunction("window.results && window.results.socket", null, { timeout: 30000 });
	const results = await ownerPage.evaluate("window.results");
	for (const name of ["check", "token", "claimJson"]) if (results[name] !== "blocked") fail(`${name}: the foreign page read p2's answer`);
	if (results.socket !== "refused") fail(`socket: ${results.socket}`);
	await new Promise((resolve) => setTimeout(resolve, 500));
	for (const path of ["/owner/check", "/owner-token", "/owner/local/challenge", "/owner/logout-all"]) {
		const requests = seen.filter((entry) => entry.url === p2 + path);
		if (!requests.some((entry) => entry.cookie)) fail(`${path} was not requested with the owner cookie (the case would prove nothing): ${JSON.stringify([...byId.values()])}`);
		for (const entry of requests) if (entry.status !== undefined && entry.status < 400) fail(`${path} answered ${entry.status} to a foreign page: ${JSON.stringify(entry)}`);
	}
	// The foreign no-cors POST did not sign the owner out, and no challenge was left it.
	const check = await owner.request.get(`${p2}/owner/check`);
	if (check.status() !== 200) fail(`the foreign logout-all ended the owner's session: ${check.status()}`);
	// Its link opens the app for the owner's browser ...
	await ownerPage.goto(`${foreignOrigin}/`);
	await ownerPage.click("#open");
	await ownerPage.waitForSelector(".composer textarea", { timeout: 60000 });
	if (new URL(ownerPage.url()).origin !== p2) fail(`the link led to ${ownerPage.url()}`);
	// ... and a browser without a p2 cookie gets the sign-in page, loaded once more from p2's origin, no cookie set.
	const fresh = await browser.newContext();
	const freshPage = await fresh.newPage();
	await freshPage.goto(`${foreignOrigin}/`);
	await freshPage.click("#open");
	await freshPage.waitForSelector("#sign-in", { timeout: 30000 });
	if (new URL(freshPage.url()).origin !== p2) fail(`the fresh browser is at ${freshPage.url()}`);
	if ((await fresh.cookies()).length) fail(`the fresh browser was given cookies: ${JSON.stringify(await fresh.cookies())}`);
	if (await freshPage.evaluate("Boolean(document.querySelector('.composer textarea'))")) fail("the fresh browser sees the app");
	console.log(JSON.stringify({ results, requests: byId.size }));
	if (process.env.P2_UI_DEBUG) console.log(JSON.stringify([...byId.values()].filter((e) => e.url.startsWith(p2) && e.site !== "same-origin")));
} finally {
	await browser.close();
	foreign.close();
}
if (failed) { console.error(`FAILED: ${failed}`); process.exit(1); }
