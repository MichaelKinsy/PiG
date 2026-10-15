// SPDX-License-Identifier: MIT
// One of ten identical, trivial extensions for the pigbench overhead bench: a tool, a command and four event handlers.
// It uses no imports, so Pi, PiG and any Pi-compatible host load it the same way.
export default function (pi: any) {
  let events = 0;
  pi.registerTool({
    name: "bench_tool_03",
    label: "Bench tool 03",
    description: "Return a fixed string. Used by the overhead bench only.",
    parameters: { type: "object", properties: { value: { type: "string" } }, additionalProperties: false },
    async execute() {
      return { content: [{ type: "text", text: "bench 03 ok" }], details: { events } };
    },
  });
  pi.registerCommand("bench-03", {
    description: "Bench command 03",
    handler: async () => {},
  });
  for (const name of ["session_start", "before_agent_start", "tool_call", "turn_end"]) {
    pi.on(name, async () => {
      events += 1;
    });
  }
}
