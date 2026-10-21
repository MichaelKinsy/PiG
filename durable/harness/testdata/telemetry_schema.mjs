// Print Pi's DURABLE_TELEMETRY_SCHEMA (packages/durable/src/harness/telemetry.ts without its import, types stripped, evaluated as written) as JSON.stringify writes it.
// usage: node telemetry_schema.mjs <path to telemetry.ts>
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const body = module.stripTypeScriptTypes(
	source.replace(/^import .*$/m, "").replace("export const DURABLE_TELEMETRY_SCHEMA", "const DURABLE_TELEMETRY_SCHEMA"),
);
const schema = new Function("defineTelemetrySchema", `${body}\nreturn DURABLE_TELEMETRY_SCHEMA;`)((definition) => definition);
process.stdout.write(JSON.stringify(schema), () => process.exit(0));
