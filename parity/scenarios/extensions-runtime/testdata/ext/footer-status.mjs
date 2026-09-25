import { readFileSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

// Use this file's own (per-binary, per-run) snapshotted directory rather than
// process.cwd(). The parity runner copies this extension into a fresh
// temp directory for each of pig/pi and for each declared run
// (parity/runner/snapshot.go snapshotExtensionPath), but launches both
// binaries from the same working directory by default. Keying the counter on
// process.cwd() let pig and pi race on one shared file across the two sides.
const generationPath = join(dirname(fileURLToPath(import.meta.url)), ".footer-status-generation");

// Advance the generation in the factory, not at module scope. Pi 0.87.1's
// /reload clears its extension cache and invokes every factory again, but
// whether the module body re-evaluates depends on the loader: jiti re-runs a
// .ts module, while this .mjs file stays in Node's native ESM cache. The
// factory call is the per-reload contract both loaders share.
function nextGeneration() {
  let generation = 1;
  try {
    generation = Number.parseInt(readFileSync(generationPath, "utf8"), 10) + 1;
  } catch {}
  writeFileSync(generationPath, String(generation));
  return generation;
}

export default function (pi) {
  const generation = nextGeneration();
  const install = (ctx) => {
    ctx.ui.setStatus("footer-status", `status-probe-${generation}`);
    ctx.ui.setFooter((_tui, _theme, footerData) => ({
      render() {
        const lines = ["footer-probe"];
        if (typeof footerData?.getExtensionStatuses === "function") {
          const statuses = [...footerData.getExtensionStatuses().entries()]
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([, value]) => String(value));
          lines.push(...statuses);
        }
        return lines;
      },
      invalidate() {},
    }));
  };

  pi.on("session_start", (_event, ctx) => install(ctx));
  pi.registerCommand("clear-footer-probe", {
    description: "Clear the custom footer while retaining keyed status.",
    handler: async (_args, ctx) => ctx.ui.setFooter(undefined),
  });
}
