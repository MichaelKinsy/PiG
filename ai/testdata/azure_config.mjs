// Run normalizeAzureBaseUrl (private, cut from Pi's packages/ai/src/api/azure-openai-config.ts) over base URLs from stdin; the result is the string or null when it throws.
// usage: node azure_config.mjs <path to azure-openai-config.ts>
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const cut = /^function normalizeAzureBaseUrl[\s\S]*?\n}\n/m.exec(source);
if (!cut) throw new Error("normalizeAzureBaseUrl not found");
const normalizeAzureBaseUrl = new Function(`${module.stripTypeScriptTypes(cut[0])}; return normalizeAzureBaseUrl;`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((baseUrl) => {
			try {
				return normalizeAzureBaseUrl(baseUrl);
			} catch (error) {
				if (!String(error?.message).startsWith("Invalid Azure OpenAI base URL: ")) throw error;
				return null;
			}
		}),
	),
);
