// pig additive (D20): Node validates its bytecode cache against source content and the running VM. Begin at SDK import, after jiti's cold Babel bootstrap, so unused compiler bytecode is not serialized for every extension.
import { enableCompileCache } from "node:module";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { getRuntimeCacheDir } from "./shims/pig-config.mjs";
let initialized = false;
const cacheDir = () => join(getRuntimeCacheDir(), "node-compile");
export function cacheImportedModules() {
  if (initialized) return;
  initialized = true;
  enableCompileCache(cacheDir());
}
// pig additive (D20): a populated cache means an earlier start already paid the first-run cost, so the runtime's own modules and the SDK graph load from bytecode instead of compiling from source on every start. A missing cache keeps the late start above, so a first run neither serializes the runtime nor Jiti's Babel up front.
export function cachePopulatedRuntime() {
  if (!initialized && existsSync(cacheDir())) cacheImportedModules();
}
