// Port of `registerTools` in .upstream/v0.99.1/packages/coding-agent/test/suite/agent-session-codemode.test.ts:37-71,
// which registers the echo, stats and screenshot tools from an extension so scripts call them with the session's tool context.
import { Type } from "typebox";

const TINY_PNG_BASE64 =
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==";

const echoSchema = Type.Object({ text: Type.String({ description: "Text to echo" }) });

export default function (pi) {
  pi.registerTool({
    name: "echo",
    label: "Echo",
    description: "Echo text back.\n\nSecond paragraph.",
    parameters: echoSchema,
    execute: async (_id, params) => ({ content: [{ type: "text", text: `echo: ${params.text}` }], details: {} }),
  });
  pi.registerTool({
    name: "stats",
    label: "Stats",
    description: "Return structured stats",
    parameters: Type.Object({}),
    outputSchema: Type.Object({ files: Type.Number(), names: Type.Array(Type.String()) }),
    execute: async () => ({
      content: [{ type: "text", text: "2 files" }],
      details: {},
      structuredContent: { files: 2, names: ["a", "b"] },
    }),
  });
  pi.registerTool({
    name: "screenshot",
    label: "Screenshot",
    description: "Return a screenshot",
    parameters: Type.Object({}),
    execute: async () => ({
      content: [
        { type: "text", text: "captured" },
        { type: "image", data: TINY_PNG_BASE64, mimeType: "image/png" },
      ],
      details: {},
    }),
  });
}
