import { writeFileSync } from "node:fs";
import { connect } from "node:net";

// The agent_settled handler and the hold command wait on a plain TCP read: they dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead. Without the address they return at once. With RPC_SETTLE_DONE the hold command writes that file once its wait returned.
function wait(env) {
  const addr = process.env[env];
  if (!addr) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const socket = connect({ host: "127.0.0.1", port: Number(addr.split(":")[1]) });
    socket.setEncoding("utf8");
    socket.on("connect", () => socket.write("waiting\n"));
    socket.on("data", () => {
      socket.destroy();
      resolve();
    });
    socket.on("error", reject);
  });
}

export default function (pi) {
  pi.on("agent_settled", () => wait("RPC_SETTLE_ADDR"));
  pi.on("input", () => wait("RPC_SETTLE_INPUT_ADDR"));
  // session_info_changed and thinking_level_select handlers: with RPC_NAME_ADDR they wait on the peer, with RPC_NAME_BUSY_MS they compute for that long without awaiting, and then they notify. Pi emits both events without awaiting them.
  for (const event of ["session_info_changed", "thinking_level_select"]) {
    pi.on(event, async (_event, ctx) => {
      const busy = Number(process.env.RPC_NAME_BUSY_MS || 0);
      if (!process.env.RPC_NAME_ADDR && !busy) return;
      for (const end = Date.now() + busy; Date.now() < end; ) {
        // computing: nothing awaits
      }
      if (process.env.RPC_NAME_ADDR) await wait("RPC_NAME_ADDR");
      ctx.ui.notify("name-handler-done:" + event, "info");
    });
  }
  pi.registerCommand("hold", { description: "Wait on a socket read", handler: async () => {
    await wait("RPC_SETTLE_ADDR");
    if (process.env.RPC_SETTLE_DONE) writeFileSync(process.env.RPC_SETTLE_DONE, "done\n");
  } });
}
