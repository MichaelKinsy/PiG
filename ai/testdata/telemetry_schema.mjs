// Print Pi's AI_TELEMETRY_SCHEMA (packages/ai/src/telemetry.ts, the shared attribute consts and the defineTelemetrySchema call, types stripped, evaluated as written) as JSON.stringify writes it.
// usage: node telemetry_schema.mjs <path to telemetry.ts>
import { readFileSync } from "node:fs";
import module from "node:module";
const source = readFileSync(process.argv[2], "utf8");
const start = source.indexOf("const provider = ");
const end = source.indexOf("\ntype RequestSpan");
if (start < 0 || end < 0) throw new Error("AI_TELEMETRY_SCHEMA not found");
const body = module.stripTypeScriptTypes(source.slice(start, end).replace("export const AI_TELEMETRY_SCHEMA", "const AI_TELEMETRY_SCHEMA"));
const schema = new Function("defineTelemetrySchema", `${body}\nreturn AI_TELEMETRY_SCHEMA;`)((definition) => definition);
process.stdout.write(JSON.stringify(schema), () => process.exit(0));
