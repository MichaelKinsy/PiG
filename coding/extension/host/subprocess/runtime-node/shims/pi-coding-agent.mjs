// Ports packages/coding-agent/src/index.ts and packages/coding-agent/src/core/sdk.ts
// Imported SDK objects own independent sessions, tools, resources and persistence.
// Built-in provider APIs use PiG's host bridge (D74); default paths use D2.
export * from "./pi-dist/pi-coding-agent/sdk-bundle/index.js";

import { getRuntime } from "../state.mjs";
export function __runtime() {
  return getRuntime();
}
