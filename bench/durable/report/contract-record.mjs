#!/usr/bin/env node
// Prints the meta.contract record for the session meta, derived from the conformance gate's report.json.
// It refuses (exit 1, nothing printed) unless the reference and every listed build passed every ready corpus row.
//
// usage: node contract-record.mjs --corpus durable/contract/corpus.toml --report <out>/report.json \
//          --core <core commit> --url <https link to the committed gate report> \
//          --build TinyGo=wasm-tinygo:<module path> [--build Go=wasm-go:<module path>] ...
import { readFileSync } from "node:fs";
import { contractRecord, parseCorpus } from "./lib/contract.mjs";
import { ResultsError } from "./lib/results.mjs";

const args = process.argv.slice(2);
const one = (f) => (args.includes(f) ? args[args.indexOf(f) + 1] : undefined);
const builds = {};
const modules = {};
for (let i = 0; i < args.length; i++) {
	if (args[i] !== "--build") continue;
	const m = /^([^=]+)=([^:]+):(.+)$/.exec(args[++i] ?? "");
	if (!m) {
		console.error(`--build wants <deck build>=<gate impl>:<module path>, got ${args[i]}`);
		process.exit(2);
	}
	builds[m[1]] = m[2];
	modules[m[1]] = readFileSync(m[3]);
}
if (!one("--corpus") || !one("--report") || Object.keys(builds).length === 0) {
	console.error("usage: node contract-record.mjs --corpus <corpus.toml> --report <report.json> --core <commit> --url <https> --build <Build>=<impl>:<module>...");
	process.exit(2);
}
try {
	const record = contractRecord({
		corpus: parseCorpus(readFileSync(one("--corpus"), "utf8")),
		report: JSON.parse(readFileSync(one("--report"), "utf8")),
		builds,
		modules,
		core: one("--core"),
		reportUrl: one("--url"),
	});
	console.log(JSON.stringify(record, null, 2));
} catch (e) {
	if (!(e instanceof ResultsError)) throw e;
	console.error(e.message);
	process.exit(1);
}
