// Shared deferred boundary fixture. The command releases the hook without timers or filesystem polling.
let markStarted;
export const started = new Promise(resolve => { markStarted = resolve; });
export default function (pi) {
  let release;
  pi.registerCommand("release-boundary", {description:"Release the pending boundary", handler:async () => { release(); }});
  pi.on("agent_before_settle", async (_event, ctx) => {
    const pending = new Promise(resolve => { release = resolve; });
    markStarted();
    ctx.ui.notify("boundary hook started", "info");
    await pending;
    return {entries:[{type:"custom_message", customType:"committed-after-abort", content:"runnable draft after abort", display:false}], continue:true};
  });
}
