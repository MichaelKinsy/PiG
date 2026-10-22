export default function (pi) {
  pi.on("model_select", async (_event, ctx) => {
    setTimeout(() => { void ctx.ui.select("pick-from-timer", ["a", "b"]); }, 0);
  });
}
