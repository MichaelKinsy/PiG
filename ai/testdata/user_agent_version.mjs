// Drive the installed pi-ai identity.js formatUserAgent and version.js PI_AI_VERSION over identity cases
// [{short, detailed, custom: string|null, version: string|null}] and print [{userAgent, default}].
import { readFileSync, realpathSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
const root = realpathSync(fileURLToPath(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url))) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/").href;
const { formatUserAgent } = await import(dist + "utils/identity.js");
const { PI_AI_VERSION } = await import(dist + "utils/version.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((c) => {
			const identity = {};
			if (c.short) identity.userAgent = "short";
			if (c.detailed) identity.userAgent = "detailed";
			if (c.custom !== null) identity.userAgent = { custom: c.custom };
			if (c.version !== null) identity.version = c.version;
			return { userAgent: formatUserAgent(identity), default: PI_AI_VERSION };
		}),
	),
);
