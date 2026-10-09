// Deterministic suspension points for Pi's prompt admission contract.
export default function (pi) {
  let holdBefore = true;
  pi.on("input", async (event, ctx) => {
    if (event.text === "hold-input") {
      await ctx.ui.select("Input suspended", ["release"]);
      return { action: "transform", text: "What is 20+22?" };
    }
  });
  pi.on("before_agent_start", async (event, ctx) => {
    if (holdBefore && event.prompt !== "What is 20+22?") {
      holdBefore = false;
      await ctx.ui.select("Preflight suspended", ["release"]);
    }
  });
}
