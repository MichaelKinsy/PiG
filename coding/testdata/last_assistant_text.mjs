// Runs the installed Pi's AgentSession.getLastAssistantText over the histories of last_assistant_text_cases.json and prints what it returns for each.
// The real method runs on an object made from AgentSession.prototype whose `messages` are the case's.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { AgentSession } = await import(pathToFileURL(root + "dist/core/agent-session.js"));
const cases = JSON.parse(readFileSync(new URL("last_assistant_text_cases.json", import.meta.url), "utf8"));
const results = cases.map(({ name, messages }) => {
  const session = Object.create(AgentSession.prototype);
  Object.defineProperty(session, "messages", { value: messages });
  return { name, text: session.getLastAssistantText() ?? null };
});
process.stdout.write(JSON.stringify(results));
