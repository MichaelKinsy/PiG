// The billed tool of "adds the usage of nested results to the codemode result" (agent-session-codemode.test.ts:229-239).
import { Type } from "typebox";

const usage = (input, cost) => ({
  input,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
  totalTokens: input,
  cost: { input: cost, output: 0, cacheRead: 0, cacheWrite: 0, total: cost },
});

export default function (pi) {
  pi.registerTool({
    name: "billed",
    label: "Billed",
    description: "Run a model",
    parameters: Type.Object({}),
    execute: async () => ({ content: [{ type: "text", text: "ran" }], details: {}, usage: usage(100, 0.25) }),
  });
}
