// Runs the exported page's scripts in a VM, as packages/coding-agent/test/export-html-cjk.test.ts does, and prints the viewer's
// safeMarkedParse output for each source text of the JSON array on stdin. argv[2] is the exported HTML file.
import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";

const html = readFileSync(process.argv[2], "utf8");
const sessionData = html.match(/<script id="session-data" type="application\/json">([^<]+)<\/script>/)?.[1];
const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((match) => match[1]);
const viewer = scripts.pop();
if (!sessionData || !viewer) throw new Error("Missing exported session data or viewer script");
const context = createContext({
	atob,
	TextDecoder,
	URLSearchParams,
	document: { getElementById: () => ({ textContent: sessionData }), querySelector: () => null },
	window: { location: { search: "" } },
});
for (const script of scripts) runInContext(script, context);
const parse = runInContext(viewer.replace("function safeMarkedParse(", "return safeMarkedParse; function safeMarkedParse("), context);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((text) => parse(text))));
