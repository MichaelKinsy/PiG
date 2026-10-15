// Run Pi's private string helpers that trim with JavaScript's String.prototype.trim over probes from stdin, cut from the sources:
//   compat.ts hasExplicitApiKey, google-vertex.ts resolveCustomBaseUrl and resolveApiKey (with isPlaceholderApiKey and GCP_VERTEX_CREDENTIALS_MARKER).
// usage: node trim_sites.mjs <compat.ts> <google-vertex.ts>
// {op: "hasKey"|"customBase"|"vertexKey", value}. Results: boolean, string or null.
import { readFileSync } from "node:fs";
import module from "node:module";
function cut(path, names, consts = []) {
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
	return module.stripTypeScriptTypes(text);
}
const compat = cut(process.argv[2], ["hasExplicitApiKey"]);
const vertex = cut(process.argv[3], ["resolveCustomBaseUrl", "resolveApiKey", "isPlaceholderApiKey"], ["GCP_VERTEX_CREDENTIALS_MARKER"]);
const { hasExplicitApiKey, resolveCustomBaseUrl, resolveApiKey } = new Function(`${compat}\n${vertex}\nreturn { hasExplicitApiKey, resolveCustomBaseUrl, resolveApiKey };`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			switch (p.op) {
				case "hasKey":
					return hasExplicitApiKey(p.value);
				case "customBase":
					return resolveCustomBaseUrl(p.value) ?? null;
				default:
					return resolveApiKey({ apiKey: p.value }) ?? null;
			}
		}),
	),
);
