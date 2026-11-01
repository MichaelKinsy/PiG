import { PI_PACKAGE_VERSION } from "./pi-version.mjs";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, PI_PACKAGE_VERSION);
const { getConfiguredSelfUpdateArgv, makeConfiguredSelfUpdateCommand } = await import(pathToFileURL(join(root, "dist/config.js")));
function argv(name, env, setting) {
	if (env === undefined) delete process.env.PI_SELF_UPDATE_COMMAND;
	else process.env.PI_SELF_UPDATE_COMMAND = env;
	console.log("CONFIGURED_ARGV " + JSON.stringify({ case: name, argv: getConfiguredSelfUpdateArgv(setting) ?? null }));
}
argv("env wins and splits on whitespace", "  nub   upgrade {installSpec} ", ["mise", "upgrade"]);
argv("blank env uses the setting", " ", ["mise", "upgrade", "npm:{packageName}"]);
argv("unset env uses the setting", undefined, ["mise", "upgrade"]);
argv("empty setting is unset", undefined, []);
argv("absent setting is unset", undefined, undefined);
const command = makeConfiguredSelfUpdateCommand(
	["wrapper", "install", "{installSpec}", "--name={packageName}", "--version", "{version}", "{unknown}"],
	{ packageName: "@earendil-works/pi-coding-agent", version: "1.2.3" },
);
console.log("CONFIGURED_COMMAND " + JSON.stringify({ command: command.command, args: command.args, display: command.display }));
