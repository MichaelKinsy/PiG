// Drive the installed pi-ai transcript replay helpers over generated system-message histories, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const t = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((messages) => ({
  current: t.getCurrentSystemMessage(messages) ?? null,
  prompt: t.getCurrentSystemPrompt(messages),
  tools: t.getCurrentTools(messages),
  declared: t.getDeclaredTools(messages),
  redefinitions: t.hasToolRedefinitions(messages),
  nonAdditive: t.hasNonAdditiveToolChanges(messages),
  initial: t.getInitialSystemMessage(messages) ?? null,
  withoutInitial: t.withoutInitialSystemMessage(messages),
  collapsed: t.collapseSystemMessages({ messages }).messages,
}));
process.stdout.write(JSON.stringify(results));
