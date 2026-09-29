// runtime-surface-a: the extension runtime surface Pi hands every extension.
// pi-agent-core is served by Pi, pi.events is one bus shared by all
// extensions whose on() returns an unsubscribe function, Pi's tool
// definition factories work at load, and console output reaches the terminal.
import * as core from "@earendil-works/pi-agent-core";
import { createReadToolDefinition, createWriteToolDefinition, generateDiffString, generateUnifiedPatch } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  console.log("surface-a: console.log at load");
  console.error("surface-a: console.error at load");
  const heard = [];
  const off = pi.events.on("surface:ping", (data) => heard.push(data.n));
  const offSession = pi.on("session_start", () => {});
  let offSnapshot;
  pi.on("session_start", () => {
    offSnapshot();
    console.error("surface-a: snapshot first");
    pi.on("session_shutdown", () => console.error("surface-a: dynamic shutdown"));
  });
  offSnapshot = pi.on("session_start", () => console.error("surface-a: snapshot second"));
  const read = createReadToolDefinition(process.cwd());
  const write = createWriteToolDefinition(process.cwd());
  console.error(`surface-a: events.on returns ${typeof off}, on returns ${typeof offSession}, registerMarkdownTransformer is ${typeof pi.registerMarkdownTransformer}`);
  console.error(`surface-a: read definition has ${Object.keys(read).join(",")}`);
  console.error(`surface-a: pi-agent-core Agent is ${typeof core.Agent}, agentLoop is ${typeof core.agentLoop}, uuidv7 is ${typeof core.uuidv7}`);
  pi.registerCommand("surface-probe", {
    description: "Probe the extension runtime surface",
    handler: async (_args, ctx) => {
      const copiedContext = { ...ctx };
      console.error(`surface-a: context spread retains ui ${copiedContext.ui === ctx.ui && !!copiedContext.ui?.theme}`);
      pi.events.emit("surface:ping", { n: 1 });
      off();
      pi.events.emit("surface:ping", { n: 2 });
      console.error(`surface-a: heard ${JSON.stringify(heard)}`);
      await write.execute("w1", { path: "surface-probe.txt", content: "surface file\n" }, undefined, undefined, ctx);
      const result = await read.execute("r1", { path: "surface-probe.txt" }, undefined, undefined, ctx);
      console.error(`surface-a: read ${JSON.stringify(result.content)}`);
      console.error(`surface-a: diff ${JSON.stringify(generateDiffString("one\ntwo\n", "one\nTWO\n", 1))}`);
      console.error(`surface-a: patch ${JSON.stringify(generateUnifiedPatch("file.txt", "one\ntwo", "one\nTWO", 1))}`);
      const agent = new core.Agent();
      console.error(`surface-a: new Agent has ${typeof agent.prompt} prompt and ${agent.state.messages.length} messages`);
    },
  });
}
