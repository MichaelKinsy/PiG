// A /reload that fails, then prompts: what the old extension instance does for them.
// Pi invalidates the old runner right after session_shutdown (agent-session.ts:3825-3826) and builds the new one only at _buildRuntime (:3844), so a reload
// that throws leaves the session on the old, stale runner. No emit method of the runner looks at staleness (runner.ts:1135-1560): the next prompts reach the old
// instance's input, before_agent_start, tool_call and tool_result handlers and its tools, and whatever of them reads its ctx gets the stale error.
// The handlers catch that error and put what they saw into the text they produce, so the transcript carries it.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

const state = (globalThis.__parityReloadPrompts ??= { generations: 0 });
const seen = (read) => {
  try {
    read();
    return "live";
  } catch (error) {
    return String(error?.message ?? error).startsWith("This extension ctx is stale") ? "stale" : "threw";
  }
};

export default function (pi) {
  const g = "g" + ++state.generations;
  pi.registerTool({
    name: "b14_plain",
    label: "b14_plain",
    description: "a tool that does not touch its context",
    parameters: { type: "object", properties: {} },
    execute: async () => ({ content: [{ type: "text", text: `plain tool ran ${g}` }] }),
  });
  pi.registerTool({
    name: "b14_ctx",
    label: "b14_ctx",
    description: "a tool that reads its context",
    parameters: { type: "object", properties: {} },
    execute: async (_id, _params, _signal, _update, ctx) => ({ content: [{ type: "text", text: `ctx tool ran ${g} ctx ${seen(() => ctx.cwd)}` }] }),
  });
  pi.on("input", async (event, ctx) => ({ action: "transform", text: `${event.text}\ninput ran ${g} ctx ${seen(() => ctx.cwd)}` }));
  // The faux provider answers from the last message's text, so the prompt closes the message.
  pi.on("before_agent_start", async (event, ctx) => ({
    message: { customType: "b14", content: `before_agent_start ran ${g} ctx ${seen(() => ctx.cwd)}\n${event.prompt}`, display: true },
  }));
  pi.on("tool_result", async (event) => {
    if (event.toolName !== "b14_plain" && event.toolName !== "b14_ctx") return undefined;
    return { content: event.content.map((part) => (part.type === "text" ? { ...part, text: `${part.text} [tool_result ${g}]` } : part)) };
  });
  pi.registerCommand("parity-reload-fails", {
    description: "Reload with a settings entry the resource loader rejects",
    handler: async (_args, ctx) => {
      const path = join(getAgentDir(), "settings.json");
      const original = readFileSync(path, "utf8");
      writeFileSync(path, JSON.stringify({ ...JSON.parse(original), prompts: ["file:///a%2Fb"] }));
      try {
        await ctx.reload();
      } catch {
        // The interactive mode shows the failure; the settings go back so only the reload's own failure is in the way.
      }
      writeFileSync(path, original);
    },
  });
}
