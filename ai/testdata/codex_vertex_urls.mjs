// Run private URL helpers of Pi's openai-codex-responses.ts (resolveCodexWebSocketUrl with resolveCodexUrl) and google-vertex.ts (baseUrlIncludesApiVersion), cut out of the sources, over probes from stdin.
// usage: node codex_vertex_urls.mjs <openai-codex-responses.ts> <google-vertex.ts>
// {op:"codexWs", baseUrl}: string, or null when new URL throws.  {op:"vertexVersion", baseUrl}: boolean.
import { readFileSync } from "node:fs";
import module from "node:module";
function cutFunctions(path, names, consts = []) {
	const source = readFileSync(path, "utf8");
	let text = "";
	for (const name of consts) {
		const m = new RegExp(`^const ${name}\\b.*$`, "m").exec(source);
		if (!m) throw new Error(`${name} not found`);
		text += `${m[0]}\n`;
	}
	for (const name of names) {
		const m = new RegExp(`^function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
		if (!m) throw new Error(`${name} not found`);
		text += `${m[0]}\n`;
	}
	return text;
}
const codexText = cutFunctions(process.argv[2], ["resolveCodexUrl", "resolveCodexWebSocketUrl"], ["DEFAULT_CODEX_BASE_URL"]);
const vertexText = cutFunctions(process.argv[3], ["baseUrlIncludesApiVersion"]);
const { resolveCodexWebSocketUrl, baseUrlIncludesApiVersion } = new Function(
	`${module.stripTypeScriptTypes(codexText)}\n${module.stripTypeScriptTypes(vertexText)}\nreturn { resolveCodexWebSocketUrl, baseUrlIncludesApiVersion };`,
)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			if (p.op === "vertexVersion") return baseUrlIncludesApiVersion(p.baseUrl);
			try {
				return resolveCodexWebSocketUrl(p.baseUrl === "" ? undefined : p.baseUrl);
			} catch (error) {
				if (!(error instanceof TypeError)) throw error;
				return null;
			}
		}),
	),
);
