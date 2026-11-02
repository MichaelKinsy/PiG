// Ports packages/coding-agent/src/index.ts: the extension imports the SDK barrel through the host's runtime and checks its runtime exports against Pi's.
import assert from "node:assert/strict";
import * as barrel from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("barrel-exports", {
    handler: async (args) => {
      const { published } = JSON.parse(args);
      const names = Object.keys(barrel);
      assert.deepEqual(published.filter((name) => !names.includes(name)), [], "the runtime barrel lacks names Pi exports");
      assert.deepEqual(names.filter((name) => !published.includes(name) && name !== "__runtime"), [], "the runtime barrel exports names Pi does not");
    },
  });
}
