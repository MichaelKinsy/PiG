// The web's "Sign in to a provider" sheet against the Go server (plugins/login): it opens and lists providers.
//   node login.mjs <base url> [server pid]
import { createRequire } from "node:module";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const browser = await chromium.launch({ args: ["--no-sandbox"] });
const page = await browser.newPage({ viewport: { width: 1280, height: 820 } });
const problems = [];
page.on("pageerror", (error) => problems.push(error.message));
page.on("console", (message) => message.type() === "error" && problems.push(message.text()));
const cdp = await page.context().newCDPSession(page);
await cdp.send("Network.enable");
const frames = [];
const keep = (direction) => (event) => {
	const raw = event.response?.payloadData ?? "";
	const data = event.response?.opcode === 2 ? Buffer.from(raw, "base64").toString("latin1") : raw;
	if (/provider-login/i.test(data)) frames.push(`${direction} ${JSON.stringify(data.slice(0, 500))}`);
};
cdp.on("Network.webSocketFrameReceived", keep("<"));
cdp.on("Network.webSocketFrameSent", keep(">"));
await page.goto(`${process.argv[2]}/`);
await page.getByText("Sign in to a provider").first().click({ timeout: 60000 });
await page.waitForSelector("[role=dialog]", { timeout: 30000 });
await page.waitForTimeout(6000);
const text = await page.evaluate(() => document.querySelector("[role=dialog]").innerText);
console.log(await page.evaluate(() => `buttons=${document.querySelectorAll("[role=dialog] button").length} body=${document.querySelector("[role=dialog] .sheet-body")?.innerText.slice(0, 200)} html=${document.querySelector("[role=dialog] .sheet-body")?.innerHTML.slice(600, 1400)}`));
await page.screenshot({ path: process.env.P2_UI_SHOT ?? "/tmp/login.png" });
console.log(frames.join("\n"));
console.log(JSON.stringify({ dialog: text.slice(0, 300), problems }));
await browser.close();
process.exit(text.length > 20 && problems.length === 0 ? 0 : 1);
