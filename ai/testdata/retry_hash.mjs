// Drive the installed pi-ai retry.js (isRetryableAssistantError, retryDelayMs) and hash.js (shortHash) over probes from stdin:
// {op: "retryable", stopReason, errorMessage} -> boolean, {op: "delay", base, cap, attempt} -> number, {op: "hash", text} -> string.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/utils/";
const { isRetryableAssistantError, retryDelayMs } = await import(pathToFileURL(dist + "retry.js"));
const { shortHash } = await import(pathToFileURL(dist + "hash.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
process.stdout.write(
	JSON.stringify(
		JSON.parse(input).map((p) => {
			if (p.op === "retryable") {
				const message = { stopReason: p.stopReason };
				if (p.errorMessage !== null) message.errorMessage = p.errorMessage;
				return isRetryableAssistantError(message);
			}
			if (p.op === "delay") return retryDelayMs({ baseDelayMs: p.base, ...(p.cap === null ? {} : { maxAgentDelayMs: p.cap }) }, p.attempt);
			return shortHash(p.text);
		}),
	),
);
