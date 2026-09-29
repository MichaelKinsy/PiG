import registerToolRenderers from "./tool-renderers.mjs";

// HTML export does not test parallel execution timing. Upstream agent-loop.ts:511-526 serializes a batch when a tool declares sequential execution, giving both runtimes the same causal update/end order without filtering events.
export default function (pi) {
  registerToolRenderers({
    registerTool(tool) {
      pi.registerTool({ ...tool, executionMode: "sequential" });
    },
  });
}
