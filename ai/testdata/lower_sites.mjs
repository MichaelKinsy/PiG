// Run Pi's private case-insensitive helpers (String.prototype.toLowerCase) over probes from stdin, cut from the sources:
//   anthropic-messages.ts toClaudeCodeName/fromClaudeCodeName, opencode-headers.ts hasHeader, and the isDeepSeek expression of openai-completions.ts detectCompat.
// usage: node lower_sites.mjs <anthropic-messages.ts> <opencode-headers.ts> <openai-completions.ts>
// {op: "toCC"|"fromCC"|"hasHeader"|"deepseek", name, names, provider}. Results: string or boolean.
import { readFileSync } from "node:fs";
import module from "node:module";
const read = (path) => readFileSync(path, "utf8");
function must(source, pattern) {
	const m = pattern.exec(source);
	if (!m) throw new Error(`${pattern} not found`);
	return m[0];
}
const anthropic = read(process.argv[2]);
const ccSource = [
	must(anthropic, /^const claudeCodeTools = \[[\s\S]*?\n\];\n/m),
	must(anthropic, /^const ccToolLookup = .*\n/m),
	must(anthropic, /^const toClaudeCodeName = .*\n/m),
	must(anthropic, /^const fromClaudeCodeName = [\s\S]*?\n};\n/m),
].join("\n");
const opencodeSource = must(read(process.argv[3]), /^function hasHeader[\s\S]*?\n}\n/m);
const completions = read(process.argv[4]);
const deepseekLine = must(completions, /^\tconst isDeepSeek = provider === "deepseek" \|\| baseUrl\.toLowerCase\(\)\.includes\("deepseek\.com"\);$/m);
const source = module.stripTypeScriptTypes(`${ccSource}\n${opencodeSource}`);
const { toClaudeCodeName, fromClaudeCodeName, hasHeader } = new Function(`${source}\nreturn { toClaudeCodeName, fromClaudeCodeName, hasHeader };`)();
const isDeepSeek = new Function("provider", "baseUrl", `${deepseekLine}\nreturn isDeepSeek;`);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			switch (p.op) {
				case "toCC":
					return toClaudeCodeName(p.name);
				case "fromCC":
					return fromClaudeCodeName(p.name, p.names.map((name) => ({ name })));
				case "hasHeader":
					return hasHeader(Object.fromEntries(p.names.map((name) => [name, "v"])), p.name);
				default:
					return isDeepSeek(p.provider, p.name);
			}
		}),
	),
);
