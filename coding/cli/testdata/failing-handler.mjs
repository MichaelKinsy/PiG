// failing-handler.mjs registers one agent_start handler that throws, so the
// runner reports an extension error on every run.
export default function failingHandlerExtension(pi) {
  pi.on("agent_start", async () => {
    throw new Error("Routing failed");
  });
}
