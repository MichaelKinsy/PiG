// Run Pi's URL-trust helpers over strings from stdin: [string] -> [{trusted, xai}] with a refusal as null. trusted-url.ts trustedHttpUrl (shared by the
// Copilot, Kimi, Meta and Radius logins) returns the href of an http(s) URL; xai.ts validateVerificationUri returns the href of an https URL or throws.
// Each function is cut out of its file (from "[export ]function <name>" to the first "}" in column 0), stripped of types, and evaluated.
// usage: node trusted_urls.mjs <oauth directory>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { join } from "node:path";
const load = (file, name) => {
	const source = readFileSync(join(process.argv[2], file), "utf8");
	const match = new RegExp(`^(?:export )?function ${name}[\\s\\S]*?\\n}\\n`, "m").exec(source);
	if (!match) throw new Error(`${name} not found in ${file}`);
	return new Function(`${stripTypeScriptTypes(match[0].replace(/^export /, ""))}; return ${name};`)();
};
const trusted = load("trusted-url.ts", "trustedHttpUrl");
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
			return { trusted: trusted(value), xai: xaiHref };
		}),
	),
);
