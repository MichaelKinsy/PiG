// The Node fixture of the socket retirement rows (socket_retirement_runtime_test.go). The test names the gate directory in PIG_RETIREMENT_GATES.
import { existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const gates = process.env.PIG_RETIREMENT_GATES;
const touch = (name) => writeFileSync(join(gates, name), "");
const exists = (name) => existsSync(join(gates, name));
const pause = () => new Promise((resolve) => setTimeout(resolve, 5));

// The factory holds while the test keeps the factory gate, before the runtime connects to the host.
export default async function (pi) {
  if (exists("factory-hold")) {
    touch("factory-holding");
    while (exists("factory-hold")) await pause();
  }
  pi.registerCommand("retire-hold", {
    description: "reload, then hold the command",
    handler: async (_args, ctx) => {
      await ctx.reload();
      touch("reloaded");
      while (!exists("release-command")) await pause();
    },
  });
  pi.registerCommand("retire-ping", {
    description: "answer from this generation",
    handler: async () => touch("pinged"),
  });
}
