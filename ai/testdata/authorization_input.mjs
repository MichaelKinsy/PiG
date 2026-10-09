// Run Pi's private parseAuthorizationInput functions (auth/oauth/anthropic.ts, openai-codex.ts, openrouter.ts) over inputs from stdin: [string] ->
// [{anthropic: {code, state}, codex: {code, state}, openrouter: code}] with an absent value as null. The functions are not exported, so each is
// cut out of its source file (it starts at "function parseAuthorizationInput" and ends at the first "}" in column 0), stripped of types, and evaluated.
// usage: node authorization_input.mjs <oauth directory>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { join } from "node:path";
const load = (file) => {
	const source = readFileSync(join(process.argv[2], file), "utf8");
	const match = /^function parseAuthorizationInput[\s\S]*?\n}\n/m.exec(source);
	if (!match) throw new Error(`parseAuthorizationInput not found in ${file}`);
	return new Function(`${stripTypeScriptTypes(match[0])}; return parseAuthorizationInput;`)();
};
const anthropic = load("anthropic.ts");
const codex = load("openai-codex.ts");
const openrouter = load("openrouter.ts");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const pair = (r) => ({ code: r.code ?? null, state: r.state ?? null });
process.stdout.write(
	JSON.stringify(JSON.parse(input).map((value) => ({ anthropic: pair(anthropic(value)), codex: pair(codex(value)), openrouter: openrouter(value) ?? null }))),
);
