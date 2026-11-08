// Screenshots of the app in both themes (empty state, a chat with code and tool output, the session options sheet) for
// before/after comparison of the brand layer.  node shots.mjs <base url> <output directory> <label>
import { mkdirSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";

const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");
const [base, out, label] = process.argv.slice(2);
if (!base || !out || !label) throw new Error("usage: node shots.mjs <base url> <output directory> <label>");
mkdirSync(out, { recursive: true });
const prompt = [
	"Here is the change:",
	"",
	"```ts",
	"export function add(a: number, b: number): number {",
	"\t// sums two numbers",
	"\treturn a + b; // 42",
	"}",
	"```",
	"",
	"Some `inline code`, **bold**, a [link](https://example.com), and a list:",
	"",
	"- first item",
	"- second item",
].join("\n");
const browser = await chromium.launch({ args: ["--no-sandbox"] });
for (const theme of ["light", "dark"]) {
	const context = await browser.newContext({ viewport: { width: 1280, height: 820 }, deviceScaleFactor: 1 });
	await context.addInitScript((value) => localStorage.setItem("pi-theme", value), theme);
	const page = await context.newPage();
	await page.goto(`${base}/`);
	await page.waitForFunction(() => document.querySelector(".cwd-pick") && document.querySelector(".model-control")?.textContent !== "Choose model", null, { timeout: 60000 });
	await page.waitForTimeout(500);
	await page.screenshot({ path: join(out, `${label}-${theme}-empty.png`) });
	await page.fill(".composer textarea", prompt);
	await page.click("button[aria-label=Send]");
	await page.waitForFunction(() => document.querySelector(".messages .assistant")?.textContent.includes("sums two numbers"), null, { timeout: 60000 });
	await page.waitForFunction(() => !document.querySelector("button[aria-label=Stop]"), null, { timeout: 60000 }).catch(() => {});
	await page.fill(".composer textarea", "bash");
	await page.click("button[aria-label=Send]");
	await page.waitForTimeout(4000);
	await page.fill(".composer textarea", "a draft being typed");
	if (theme === "light") console.log(await page.evaluate(() => { const p = document.querySelector(".messages .assistant p"); const chain = []; for (let e = p; e && e.tagName !== "BODY"; e = e.parentElement) chain.push(`${e.tagName}.${e.className}`); return `${chain.join(" < ")} line-height=${getComputedStyle(p).lineHeight} font-size=${getComputedStyle(p).fontSize} margin=${getComputedStyle(p).marginBottom}`; }));
	await page.screenshot({ path: join(out, `${label}-${theme}-chat.png`) });
	await page.fill(".composer textarea", "");
	await page.getByText(/used bash/).first().click().catch((error) => console.error("tool row:", error.message));
	await page.waitForTimeout(600);
	await page.screenshot({ path: join(out, `${label}-${theme}-tool.png`) });
	await page.keyboard.press("Escape");
	await page.waitForTimeout(400);
	await page.fill(".composer textarea", "");
	await page.click("button[aria-label='Session options']").catch((error) => console.error("options:", error.message));
	await page.waitForTimeout(800);
	await page.screenshot({ path: join(out, `${label}-${theme}-settings.png`) });
	await context.close();
}
await browser.close();
console.log("ok");
