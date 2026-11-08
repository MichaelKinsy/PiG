// The branded web UI's screenshot for the visual gate, in a chosen theme (the page's own "pi-theme" choice, as the sidebar's
// toggle stores it) at the native window's content size, after the empty state's banner is drawn and the lists settled.
//   P2_UI_NODE_PATH=<node_modules with playwright-core> node webshot.mjs <base url> <light|dark> <out.png> [wait for text]
import { createRequire } from "node:module";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const [base, theme, out, text] = process.argv.slice(2); // base with /s/<id>: an open session, the chat screen
if (!base || !theme || !out) throw new Error("usage: node webshot.mjs <base url> <light|dark> <out.png> [text]");
const browser = await chromium.launch({
	args: ["--no-sandbox"],
	...(process.env.P2_CHROME_BIN ? { executablePath: process.env.P2_CHROME_BIN } : {}),
});
const context = await browser.newContext({ viewport: { width: 1280, height: 812 }, deviceScaleFactor: 1 });
await context.addInitScript((value) => localStorage.setItem("pi-theme", value), theme);
const page = await context.newPage();
const chat = new URL(base).pathname.startsWith("/s/");
await page.goto(chat ? base : `${base}/`);
if (!chat) await page.waitForSelector(".brand-banner", { timeout: 60000 });
if (text) await page.getByText(text).first().waitFor({ timeout: 30000 });
if (process.env.P2_WEB_CLICK) {
	// the tool cards: open the activity rows named by the selector (the last one), as the native half opens its own
	await page.locator(process.env.P2_WEB_CLICK).last().click();
	await page.waitForTimeout(600);
}
if (process.env.P2_WEB_AFTER) await page.getByText(process.env.P2_WEB_AFTER).first().waitFor({ timeout: 30000 });
await page.evaluate(() => document.fonts.ready);
await page.waitForTimeout(800);
await page.screenshot({ path: out });
await browser.close();
