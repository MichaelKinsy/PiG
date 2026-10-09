// Drive the installed pi-ai error-body.js over probes from stdin: {op: "stringify", json} -> string (the JSON text parsed in JS and written by
// safeJsonStringify), {op: "truncate", text, max} -> string, {op: "format", status, body, message, carries, prefix} -> string.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { safeJsonStringify, truncateErrorText, formatProviderError } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/utils/error-body.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			if (p.op === "stringify") return safeJsonStringify(JSON.parse(p.json));
			if (p.op === "truncate") return truncateErrorText(p.text, p.max);
			const norm = { message: p.message, messageCarriesBody: p.carries };
			if (p.status !== null) norm.status = p.status;
			if (p.body !== null) norm.body = p.body;
			return formatProviderError(norm, p.prefix === null ? undefined : p.prefix);
		}),
	),
);
