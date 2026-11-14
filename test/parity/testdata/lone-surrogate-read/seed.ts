// A tool, entries, a message and event results whose strings hold a lone UTF-16 surrogate.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const U = "\ud83d";

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "echo",
    label: "echo",
    description: "echo",
    parameters: { type: "object", properties: { p: { type: "string" } } },
    async execute() {
      return { content: [{ type: "text", text: `TR[${U}]` }], details: { d: `TD[${U}]` } };
    },
  });
  pi.on("session_start", async () => {
    try { pi.appendEntry(`CT[${U}]`, { k: `AE[${U}]` }); } catch (error) { console.error("appendEntry", error); }
    try { pi.sendMessage({ customType: `CM[${U}]`, content: `SM[${U}]`, display: true, details: { d: `SD[${U}]` } }); } catch (error) { console.error("sendMessage", error); }
  });
  pi.on("input", async (event) => ({ action: "transform", text: `${event.text} IN[${U}]` }));
  pi.on("before_agent_start", async (event) => ({ systemPrompt: `${event.systemPrompt} SP[${U}]` }));
  pi.on("tool_result", async (event) => ({ content: [...event.content, { type: "text", text: `TX[${U}]` }] }));
}
