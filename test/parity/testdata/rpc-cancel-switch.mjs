export default function rpcCancelSwitch(pi) {
  pi.on("session_before_switch", (event, ctx) => {
    ctx.ui.notify(`before-switch:${event.reason}`, "info");
    return { cancel: true };
  });
}
