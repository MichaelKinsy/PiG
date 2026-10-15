// Runs the pinned Pi CodemodeSandbox on stores read from stdin. A Worker started from the eval that hosts the Pi oracle inherits its --input-type and refuses to start, so the Go test runs this file in a plain process.
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = process.env.PI_ORACLE_ROOT;
const { CodemodeSandbox } = await import(pathToFileURL(root + "/pi-codemode/index.js"));
const { loadQuickJSWasm } = await import(pathToFileURL(root + "/pi-codemode/wasm.js"));
const wasm = loadQuickJSWasm(process.env.QUICKJS_WASM);
const units = (key) => Array.from({ length: key.length }, (_, i) => key.charCodeAt(i));
const out = [];
for (const p of JSON.parse(readFileSync(0, "utf8"))) {
	const store = {};
	p.keys.forEach((units, i) => {
		Object.defineProperty(store, String.fromCharCode(...units), { value: JSON.parse(p.values[i]), enumerable: true, writable: true, configurable: true });
	});
	const sandbox = new CodemodeSandbox({ timeoutMs: 10000, wasm });
	try {
		const result = await sandbox.execute(p.script, { store });
		const set = result.ok ? Object.entries(result.storeWrites.set).map(([key, value]) => "[" + units(key).join(" ") + "]=" + JSON.stringify(value)).sort() : [];
		const deleted = result.ok ? result.storeWrites.delete.map((key) => "[" + units(key).join(" ") + "]").sort() : [];
		const writes = result.ok ? "set [" + set.join(" ") + "] delete [" + deleted.join(" ") + "]" : "";
		out.push({ value: result.ok ? JSON.stringify(result.value) : "", err: result.ok ? "" : String(result.error && result.error.message), writes });
	} finally {
		await sandbox.close();
	}
}
process.stdout.write(JSON.stringify(out));
