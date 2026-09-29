import * as module from "node:module";
import { resolve } from "./loader.mjs";

// Pi's CLI discards process warnings (cli/setup.ts).
process.emitWarning = () => {};

// Synchronous hooks also cover native require(ESM). Node 22.13 predates registerHooks and needs the asynchronous hook registration API.
if (typeof module.registerHooks === "function") {
  module.registerHooks({ resolve });
} else {
  module.register(new URL("./loader.mjs", import.meta.url));
}
