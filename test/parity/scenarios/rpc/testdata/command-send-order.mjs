// An extension command starts a turn with pi.sendUserMessage without awaiting it. The before_agent_start handler's setStatus marks where the run's preparation lands among the RPC records.
export default function (pi) {
  pi.on("before_agent_start", (_event, ctx) => {
    ctx.ui.setStatus("order", "busy");
  });
  pi.registerCommand("sync-user", {
    handler: (_args, _ctx) => {
      pi.sendUserMessage("Say hello");
    },
  });
}
