// Bundle Pi's library entries without copying the shared terminal, keybinding, stream, UUID, or native-loader state into a second module instance.
import { bundleRuntimePackage } from "./bundle-pi-package.mjs";
const [shims, output] = process.argv.slice(2);
await bundleRuntimePackage(shims, "pi-ai", { index: "index.js", compat: "compat.js", providers: "providers/all.js" }, {
  output: output ? `${output}/pi-ai` : undefined,
  json: true,
  external: ["auth/oauth/load.js", "api/bedrock-converse-stream.lazy.js", "api/lazy.js", "utils/event-stream.js", "utils/uuid.js", "utils/provider-env.js", "providers/radius-config.js"],
});
await bundleRuntimePackage(shims, "pi-tui", { index: "index.js" }, {
  output: output ? `${output}/pi-tui` : undefined,
  external: ["tui.js", "keybindings.js", "keys.js", "terminal-image.js", "utils.js", "native-module-path.js", "native-platform.js"],
});
