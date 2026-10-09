// Run Pi's private URL-trust helpers over strings from stdin: [string] -> [{kimi, meta, xai}] with a refusal as null. kimi-coding.ts trustedHttpUrl and
// meta.ts trustedHttpUrl return the href of an http(s) URL; xai.ts validateVerificationUri returns the href of an https URL or throws. The functions are
// not exported, so each is cut out of its file (from "function <name>" to the first "}" in column 0), stripped of types, and evaluated.
// usage: node trusted_urls.mjs <oauth directory>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { join } from "node:path";
const load = (file, name) => {
	const source = readFileSync(join(process.argv[2], file), "utf8");
	const match = new RegExp(`^function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found in ${file}`);
	return new Function(`${stripTypeScriptTypes(match[0])}; return ${name};`)();
};
const kimi = load("kimi-coding.ts", "trustedHttpUrl");
const meta = load("meta.ts", "trustedHttpUrl");
const xai = load("xai.ts", "validateVerificationUri");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((value) => {
			let xaiHref = null;
			try {
				xaiHref = xai(value);
			} catch {}
			return { kimi: kimi(value), meta: meta(value), xai: xaiHref };
		}),
	),
);
