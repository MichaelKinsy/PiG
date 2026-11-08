import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { fileURLToPath } from "node:url";

/** Types stripped by this loader for the TypeScript files under node_modules, which Node's own stripping refuses. */
export async function load(url, context, nextLoad) {
	if (url.startsWith("file:") && url.endsWith(".ts") && url.includes("/node_modules/")) {
		return { format: "module", source: stripTypeScriptTypes(readFileSync(fileURLToPath(url), "utf8")), shortCircuit: true };
	}
	return nextLoad(url, context);
}
