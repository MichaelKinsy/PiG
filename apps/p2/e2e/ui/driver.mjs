// A browser driven over stdin/stdout, for the Go tests that interleave server-side checks with what the page does (the
// Browser of Pi's test/p2/browser.ts): one JSON request per line {id, op, ...}, one JSON answer per line {id, value | error}.
//   node driver.mjs [chromium flag...]
// Ops: open {url}, click {selector}, wait {expression} (polled until it is truthy), evaluate {expression}, close.
import { createInterface } from "node:readline";
import { createRequire } from "node:module";
import { join } from "node:path";

// playwright-core is installed outside the repository (P2_UI_NODE_PATH: the node_modules holding it).
const { chromium } = createRequire(join(process.env.P2_UI_NODE_PATH ?? process.cwd(), "x.js"))("playwright-core");

const browser = await chromium.launch({ args: ["--no-sandbox", ...process.argv.slice(2)] });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, permissions: ["microphone"] });
const page = await context.newPage();
const problems = [];
page.on("pageerror", (error) => problems.push(`pageerror: ${error.message}`));

const operations = {
	open: ({ url }) => page.goto(url, { waitUntil: "load" }).then(() => null),
	click: ({ selector }) => page.click(selector, { timeout: 30_000 }).then(() => null),
	// Polled through the debugger (as Pi's Browser does): the page's CSP forbids the string evaluation of `waitForFunction`.
	wait: async ({ expression }) => {
		const deadline = Date.now() + 30_000;
		while (!(await page.evaluate(`Boolean(${expression})`))) {
			if (Date.now() > deadline) throw new Error(`timed out waiting for ${expression}`);
			await new Promise((resolve) => setTimeout(resolve, 25));
		}
		return null;
	},
	evaluate: ({ expression }) => page.evaluate(expression),
	problems: () => problems,
};

const lines = createInterface({ input: process.stdin });
for await (const line of lines) {
	const request = JSON.parse(line);
	if (request.op === "close") break;
	try {
		const value = await operations[request.op](request);
		console.log(JSON.stringify({ id: request.id, value: value ?? null }));
	} catch (error) {
		console.log(JSON.stringify({ id: request.id, error: String(error.message ?? error) }));
	}
}
await browser.close();
