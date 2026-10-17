// Run Pi's private getAccountId (and decodeJwt) from auth/oauth/openai-codex.ts over tokens from stdin: [string] -> [string] with null as "".
// The functions and the JWT_CLAIM_PATH constant are cut out of the file (from "function <name>" to the first "}" in column 0), stripped of types, and evaluated.
// usage: node codex_account_id.mjs <path to openai-codex.ts>
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = (pattern) => {
	const match = pattern.exec(source);
	if (!match) throw new Error(`${pattern} not found`);
	return stripTypeScriptTypes(match[0]);
};
const parts = [/^const JWT_CLAIM_PATH[^\n]*\n/m, /^function decodeJwt[\s\S]*?\n}\n/m, /^function getAccountId[\s\S]*?\n}\n/m].map(cut);
const getAccountId = new Function(`${parts.join("\n")}; return getAccountId;`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map((token) => getAccountId(token) ?? "")));
