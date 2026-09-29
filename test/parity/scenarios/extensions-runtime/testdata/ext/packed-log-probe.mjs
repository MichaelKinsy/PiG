import fs from "node:fs";
import os from "node:os";
import { fileURLToPath } from "node:url";

export default function (pi) {
  const counter = fileURLToPath(import.meta.url) + ".generation";
  pi.on("session_start", (_event, ctx) => {
    const generation = fs.existsSync(counter) ? Number(fs.readFileSync(counter, "utf8")) + 1 : 1;
    fs.writeFileSync(counter, String(generation));
    const logs = fs.readdirSync(os.tmpdir()).filter(name => name.startsWith("pig-packed-") && name.endsWith(".log"));
    // D20: PiG owns one active packed-process log; Pi loads in-process and owns none.
    // session_start follows completed reload, including the old runtime's teardown.
    const obsolete = logs.length - (process.env.PIG_EXT_PACKED_CELL ? 1 : 0);
    ctx.ui.setFooter(() => ({
      render: () => [`packed-log-probe=${generation}:obsolete=${obsolete}`],
      invalidate() {},
    }));
  });
}
