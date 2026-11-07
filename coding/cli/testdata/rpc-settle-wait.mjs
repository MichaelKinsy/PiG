import { connect } from "node:net";

// The agent_settled handler and the hold command wait on a plain TCP read: they dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. Without RPC_SETTLE_ADDR they return at once.
function wait() {
  if (!process.env.RPC_SETTLE_ADDR) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const socket = connect({ host: "127.0.0.1", port: Number(process.env.RPC_SETTLE_ADDR.split(":")[1]) });
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
  pi.on("agent_settled", wait);
  pi.registerCommand("hold", { description: "Wait on a socket read", handler: wait });
}
