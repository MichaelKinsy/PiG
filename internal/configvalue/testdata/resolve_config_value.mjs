// Drive the installed Pi's resolve-config-value, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const c = await import(pathToFileURL(root + "dist/core/resolve-config-value.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { configs, env, headers } = JSON.parse(input);
const attempt = (fn) => { try { const v = fn(); return { ok: v === undefined ? null : v }; } catch (error) { return { err: String(error && error.message || error) }; } };
process.stdout.write(JSON.stringify({
	configs: configs.map((config) => ({
		name: c.getConfigValueEnvVarName(config) ?? null,
		names: c.getConfigValueEnvVarNames(config),
		missing: c.getMissingConfigValueEnvVarNames(config, env),
		command: c.isCommandConfigValue(config),
		configured: c.isConfigValueConfigured(config, env),
		value: c.resolveConfigValueUncached(config, env) ?? null,
		thrown: attempt(() => c.resolveConfigValueOrThrow(config, "the key", env)),
	})),
	headers: c.resolveHeaders(headers, env) ?? null,
}));
