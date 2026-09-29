// Module-level state in an .mjs extension. Pi 0.87.1's /reload clears its extension cache and invokes the factory again, but the .mjs module stays in Node's native ESM cache, so this counter keeps counting across reloads.
let moduleFactoryCalls = 0;

export default function (pi) {
  moduleFactoryCalls++;
  const calls = moduleFactoryCalls;
  pi.on("session_start", (_event, ctx) => ctx.ui.setStatus("a-mjs", `mjs-module-calls=${calls}`));
}
