// Replay each case's messages through the installed Pi's getCurrentSystemMessage and report the TypeError message it throws, or "ok". system_replay_error_test.go compares PiG's currentSystemMessageError with this report.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const pi = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(pi + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getCurrentSystemMessage } = await import(pathToFileURL(pi + "node_modules/@earendil-works/pi-ai/dist/index.js"));

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const report = JSON.parse(input).map((messages) => {
	try {
		getCurrentSystemMessage(messages);
		return "ok";
	} catch (error) {
		if (!(error instanceof TypeError)) throw error;
		return error.message;
	}
});
process.stdout.write(JSON.stringify(report));
