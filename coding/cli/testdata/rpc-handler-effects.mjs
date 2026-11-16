// The session_info_changed and thinking_level_select handlers compute (RPC_EFFECT_BUSY_MS of busy loop, awaiting nothing) and then call five host APIs in their synchronous part. Pi writes each call as it is made, so everything a handler writes precedes the reply of the command that emitted the event.
function busy() {
  const end = Date.now() + Number(process.env.RPC_EFFECT_BUSY_MS || 0);
  while (Date.now() < end);
}

function effects(event, ctx) {
  busy();
  ctx.ui.notify(`handler-done:${event}`, "info");
  ctx.ui.setStatus("handler", `status:${event}`);
  ctx.ui.setWidget("handler", [`widget:${event}`]);
  ctx.ui.setTitle(`title:${event}`);
  ctx.ui.setEditorText(`editor:${event}`);
}

export default function (pi) {
  pi.on("session_info_changed", (_event, ctx) => effects("session_info_changed", ctx));
  pi.on("thinking_level_select", (_event, ctx) => effects("thinking_level_select", ctx));
}
