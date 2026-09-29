// pig additive (D20): Node validates its bytecode cache against source content and the running VM. Begin at SDK import, after jiti's cold Babel bootstrap, so unused compiler bytecode is not serialized for every extension.
import { enableCompileCache } from "node:module";
import { join } from "node:path";
import { getRuntimeCacheDir } from "./shims/pig-config.mjs";
let initialized = false;
export function cacheImportedModules() {
  if (initialized) return;
  initialized = true;
  enableCompileCache(join(getRuntimeCacheDir(), "node-compile"));
}
