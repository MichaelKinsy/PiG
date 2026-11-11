// Drive the installed Pi's isInstallTelemetryEnabled for each PI_TELEMETRY value and install-telemetry setting, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { isInstallTelemetryEnabled } = await import(pathToFileURL(root + "dist/core/telemetry.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(JSON.stringify(JSON.parse(input).map(({ env, setting }) => {
	const settingsManager = { getEnableInstallTelemetry: () => setting };
	if (env === null) {
		delete process.env.PI_TELEMETRY;
		return isInstallTelemetryEnabled(settingsManager);
	}
	process.env.PI_TELEMETRY = env;
	return isInstallTelemetryEnabled(settingsManager);
})));
