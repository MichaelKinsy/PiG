import { appendFileSync } from "node:fs";

// A second extension file. Both extension files pack into one Node runtime
// process, each with its own connection, so its commands settle apart from
// rpc-shutdown.mjs's only per connection. micro2 settles through microtasks
// only, but its continuation keeps the shared event loop busy for a while,
// while the other connection's command has already been reported suspended.
export default function (pi) {
  pi.registerCommand("micro2", {
    description: "Settle through microtasks",
    handler: async () => {
      await Promise.resolve();
      await null;
      const until = Date.now() + 20;
      while (Date.now() < until);
      appendFileSync(process.env.RPC_SHUTDOWN_REPORT, "micro2\n");
    },
  });
  // ns2 replaces the Session from this extension, so its response travels on this connection while the runtime_drained report of a never-settling quit handler goes out on the first runtime's connection.
  pi.registerCommand("ns2", {
    description: "Replace the Session",
    handler: async (_args, ctx) => {
      await ctx.newSession();
      appendFileSync(process.env.RPC_SHUTDOWN_REPORT, "ns2-done\n");
    },
  });
}
