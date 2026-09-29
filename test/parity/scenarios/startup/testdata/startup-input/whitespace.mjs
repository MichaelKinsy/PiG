export default function (pi) {
  const points = [];
  pi.on("session_start", (_event, ctx) => ctx.ui.notify("SUBMIT WHITESPACE READY", "info"));
  pi.on("input", (event, ctx) => {
    points.push(Array.from(event.text, character => character.codePointAt(0)));
    ctx.ui.notify(`SUBMIT_POINTS ${JSON.stringify(points)}`, "info");
    return { action: "handled" };
  });
}
