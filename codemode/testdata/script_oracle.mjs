// Runs the pinned Pi CodemodeSandbox on the scripts read from stdin (see store_oracle.mjs for why this is a separate process).
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = process.env.PI_ORACLE_ROOT;
const { CodemodeSandbox } = await import(pathToFileURL(root + "/pi-codemode/index.js"));
const { loadQuickJSWasm } = await import(pathToFileURL(root + "/pi-codemode/wasm.js"));
const wasm = loadQuickJSWasm(process.env.QUICKJS_WASM);
/* An unpaired surrogate cannot cross the Go JSON decoder, so output text names each one. */
const plain = (text) => text.replace(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g, (unit) => "<U+" + unit.charCodeAt(0).toString(16) + ">");
const out = [];
for (const script of JSON.parse(readFileSync(0, "utf8"))) {
	const sandbox = new CodemodeSandbox({ timeoutMs: 10000, wasm });
	try {
		const result = await sandbox.execute(script);
		out.push({
			ok: result.ok,
			value: result.ok ? (result.value === undefined ? "<undefined>" : JSON.stringify(result.value)) : "",
			output: result.output.map((item) => (item.type === "text" ? (item.console ? "console:" : "text:") + plain(item.text) : "image:" + item.mimeType + ":" + item.data)),
			error: result.ok ? "" : result.error.kind + "|" + (result.error.name ?? "") + "|" + plain(result.error.message),
		});
	} finally {
		await sandbox.close();
	}
}
process.stdout.write(JSON.stringify(out));
