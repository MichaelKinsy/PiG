import fs from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const sourcePath = fileURLToPath(import.meta.url);
const markerPath = path.join(path.dirname(sourcePath), "reload-shortcut.marker");

export default function register(pi) {
  const marker = fs.readFileSync(markerPath, "utf8").trim();
  pi.registerShortcut("ctrl+shift+right", {
    description: "Report the loaded shortcut generation",
    handler: (ctx) => ctx.ui.notify(`shortcut-${marker}`, "info"),
  });

  pi.registerCommand("parity-swap-shortcut", {
    description: "Rewrite this fixture to its replacement generation",
    handler: (_args, ctx) => {
      fs.writeFileSync(markerPath, "new\n");
      fs.appendFileSync(sourcePath, "\n");
      ctx.ui.notify("shortcut-source-swapped", "info");
    },
  });
}
