// Drive the installed pi-ai overflow.js isContextOverflow and isRecoverableLength over probes from stdin:
// [{stopReason, errorMessage, provider, usage: {input, cacheRead, output}, contextWindow, desired}] -> [{overflow, recoverable}].
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { isContextOverflow, isRecoverableLength } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/utils/overflow.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			const message = { stopReason: p.stopReason, provider: p.provider, usage: p.usage };
			if (p.errorMessage !== null) message.errorMessage = p.errorMessage;
			return { overflow: isContextOverflow(message, p.contextWindow === 0 ? undefined : p.contextWindow), recoverable: isRecoverableLength(message, p.desired) };
		}),
	),
);
