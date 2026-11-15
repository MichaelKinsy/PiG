// A `node --import` preload that serves the upstream responses recorded by `go run ./cmd/gen-models -record <dir>` (the Go
// generator's fixtures, index.json plus one body per response) and fails any other request, as Pi's own tests do with
// packages/ai/test/catalog-generation-utils.ts.
import { readFileSync } from "node:fs";
import { join } from "node:path";

const dir = process.env.CATALOG_FIXTURES;
if (!dir) throw new Error("CATALOG_FIXTURES is not set");
const entries = new Map(JSON.parse(readFileSync(join(dir, "index.json"), "utf8")).map((entry) => [entry.url, entry]));
globalThis.fetch = async (input) => {
	const url = String(input instanceof Request ? input.url : input);
	const entry = entries.get(url);
	if (!entry) throw new Error(`Unexpected fetch: ${url}`);
	return new Response(readFileSync(join(dir, entry.file)), { status: entry.status, headers: { "content-type": "application/json" } });
};

// CATALOG_FETCHED_AT: the instant `new Date()` and Date.now() give, the fetch time of the committed recording, so the generator writes the
// manifest `generatedAt` the recording says and not the clock's.
const fetchedAt = process.env.CATALOG_FETCHED_AT;
if (fetchedAt) {
	const fixed = Date.parse(fetchedAt);
	if (Number.isNaN(fixed)) throw new Error(`CATALOG_FETCHED_AT is not a time: ${fetchedAt}`);
	const RealDate = Date;
	globalThis.Date = class extends RealDate {
		constructor(...args) {
			if (args.length === 0) super(fixed);
			else super(...args);
		}
		static now() {
			return fixed;
		}
	};
}
