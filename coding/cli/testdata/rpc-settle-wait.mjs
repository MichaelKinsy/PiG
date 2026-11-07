import { connect } from "node:net";

// The agent_settled handler and the hold command wait on a plain TCP read: they dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead. Without the address they return at once.
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
  pi.registerCommand("hold", { description: "Wait on a socket read", handler: () => wait("RPC_SETTLE_ADDR") });
}
