// Replay each case's system messages as Pi's _restoreToolsFromTranscript does and report the TypeError message it throws, or "ok". tool_replay_error_test.go compares PiG's ToolReplayError with this report.
// The collection of the tools the branch ever added or removed is the expression of agent-session.ts _restoreToolsFromTranscript (Pi bd3c9b8237); getCurrentTools is the installed Pi's.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const pi = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(pi + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getCurrentTools } = await import(pathToFileURL(pi + "node_modules/@earendil-works/pi-ai/dist/index.js"));

let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const report = JSON.parse(input).map((systemMessages) => {
	try {
		new Set(
			systemMessages
				.flatMap((message) => [...(message.toolsAdded ?? []), ...(message.toolsRemoved ?? [])])
				.map((tool) => tool.name),
		);
		getCurrentTools(systemMessages);
		return "ok";
	} catch (error) {
		if (!(error instanceof TypeError)) throw error;
		return error.message;
	}
});
process.stdout.write(JSON.stringify(report));
