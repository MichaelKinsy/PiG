// SPDX-License-Identifier: MIT
// One of ten identical, trivial extensions for the pigbench overhead bench: a tool, a command and four event handlers.
// It uses no imports, so Pi, PiG and any Pi-compatible host load it the same way.
export default function (pi: any) {
  let events = 0;
  pi.registerTool({
    name: "bench_tool_02",
    label: "Bench tool 02",
    description: "Return a fixed string. Used by the overhead bench only.",
    parameters: { type: "object", properties: { value: { type: "string" } }, additionalProperties: false },
    async execute() {
      return { content: [{ type: "text", text: "bench 02 ok" }], details: { events } };
    },
  });
  pi.registerCommand("bench-02", {
    description: "Bench command 02",
    handler: async () => {},
  });
  for (const name of ["session_start", "before_agent_start", "tool_call", "turn_end"]) {
    pi.on(name, async () => {
      events += 1;
    });
  }
}
