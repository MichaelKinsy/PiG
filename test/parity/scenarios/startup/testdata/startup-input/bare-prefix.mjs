export default function (pi) {
  const inputs = [];
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.notify("BARE PREFIX PROBE READY", "info");
  });
  pi.on("input", (event, ctx) => {
    inputs.push(event.text);
    ctx.ui.notify(`BARE PREFIXES: ${JSON.stringify(inputs)}`, "info");
    return { action: "handled" };
  });
}
