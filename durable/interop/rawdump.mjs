// A canonical dump of a store's tables, read with SQL: every row, JSON columns parsed and written with sorted keys.
// Wall-clock fields are zeroed so two runs of one script compare equal.
import { DatabaseSync } from "node:sqlite";

// durationMs keeps its key, so a missing duration still differs from a measured one.
const CLOCK_KEYS = new Set(["timestamp", "durationMs"]);

export function canonical(value, key) {
	if (Array.isArray(value)) return value.map((item) => canonical(item));
	if (value !== null && typeof value === "object") {
		return Object.fromEntries(
			Object.keys(value)
				.sort()
				.map((name) => [name, canonical(value[name], name)]),
		);
	}
	if (CLOCK_KEYS.has(key)) return 0;
	if (key === "sessionId") return "<uuid>";
	if (key === "api" && typeof value === "string" && (value === "faux" || value.startsWith("faux:"))) return "faux:*";
	return value;
}

export function rawDump(path) {
	const db = new DatabaseSync(path, { readOnly: true });
	const dump = {};
	const tables = ["durable_metadata", "durable_schema", "record_ids", "conversations", "entries", "tasks", "submissions", "documents", "document_revisions"];
	for (const table of tables) {
		const order = table === "document_revisions" ? "document_id, seq" : table === "durable_metadata" || table === "durable_schema" ? "singleton" : "id";
		dump[table] = db.prepare(`SELECT * FROM ${table} ORDER BY ${order}`).all().map((row) => {
			const out = {};
			for (const [name, value] of Object.entries(row)) {
				if ((name === "record" || name === "content") && typeof value === "string") out[name] = canonical(JSON.parse(value));
				else out[name] = typeof value === "bigint" ? Number(value) : value;
			}
			return out;
		});
	}
	db.close();
	return dump;
}

if (process.argv[1].endsWith("rawdump.mjs")) process.stdout.write(`${JSON.stringify(rawDump(process.argv[2]), null, 1)}\n`);
