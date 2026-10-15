// Run Pi's packages/ai/src/providers/radius-config.ts over probes from stdin.
// usage: node radius_config.mjs <path to radius-config.ts>
// {op: "norm", value} -> normalizeRadiusGatewayUrl; {op: "trunc", value} -> truncateHttpBody (private, cut from the source);
// {op: "config", gateway: <JSON text or null for absent>} -> {ok, baseUrl, ids, models} of getRadiusCredentialConfig and the provider/baseUrl/api bound by getRadiusModels.
// A number JSON cannot hold (Infinity, -Infinity) is written as its string.
import { readFileSync } from "node:fs";
import module from "node:module";
import { pathToFileURL } from "node:url";
const path = process.argv[2];
const { normalizeRadiusGatewayUrl, getRadiusCredentialConfig, getRadiusModels } = await import(pathToFileURL(path).href);
const source = readFileSync(path, "utf8");
const cut = /^function truncateHttpBody[\s\S]*?\n}\n/m.exec(source);
if (!cut) throw new Error("truncateHttpBody not found");
const truncateHttpBody = new Function(`${module.stripTypeScriptTypes(cut[0])}; return truncateHttpBody;`)();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			if (p.op === "norm") return normalizeRadiusGatewayUrl(p.value);
			if (p.op === "trunc") return truncateHttpBody(p.value);
			const credential = p.gateway === null ? { type: "oauth" } : { type: "oauth", gatewayConfig: JSON.parse(p.gateway) };
			const config = getRadiusCredentialConfig(credential);
			if (!config) return { ok: false };
			return { ok: true, baseUrl: config.baseUrl, ids: config.models.map((m) => m.id), models: config.models, bound: getRadiusModels("radius", credential).map((m) => [m.id, m.provider, m.baseUrl, m.api]) };
		}),
		(_key, value) => (typeof value === "number" && !Number.isFinite(value) ? String(value) : value),
	),
);
