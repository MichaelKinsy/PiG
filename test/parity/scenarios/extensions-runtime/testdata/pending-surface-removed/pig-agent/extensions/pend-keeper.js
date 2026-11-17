import { existsSync, readFileSync, writeFileSync, unlinkSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

// The extension that stays. Counts its own session_start events in a file and shows the count as a footer status, so each reload leaves a distinct line.
// /probe <surface> reports whether an owner's late marker exists now and appends the answer to probe-log; /probe all prints, per surface, the answer
// at the time of its probe and the answer now.
const state = (n) => (existsSync(join(getAgentDir(), `late-${n}.marker`)) ? "written" : "never");
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    const file = join(getAgentDir(), "keeper-starts");
    const n = (existsSync(file) ? Number(readFileSync(file, "utf8")) : 0) + 1;
    writeFileSync(file, String(n));
    ctx.ui.setStatus("keeper", `keeper-start=${n}`);
    if (n > 1) {
      const all = () => ["custom", "select", "confirm", "input", "editor"].map((m) => `${m}=${state(m)}`).join(" ");
      setTimeout(() => ctx.ui.setStatus("probe-a", `late-markers@3s: ${all()}`), 3000);
      setTimeout(() => ctx.ui.setStatus("probe-b", `late-markers@7s: ${all()}`), 7000);
    }
  });
  pi.registerCommand("probe", {
    description: "Report whether an owner's late marker was written",
    handler: async (args, ctx) => {
      const s = args.trim();
      const log = join(getAgentDir(), "probe-log");
      if (s === "all") {
        const seen = existsSync(log) ? readFileSync(log, "utf8").split("\n") : [];
        const parts = ["custom", "select", "confirm", "input", "editor"].map((n) => `${n}=${(seen.find((l) => l.startsWith(`${n}=`)) ?? `${n}=?`).slice(n.length + 1)}/${state(n)}`);
        const line = `probe-final: ${parts.join(" ")}`;
        ctx.ui.setStatus("probe", line);
        ctx.ui.notify(line, "info");
        return;
      }
      writeFileSync(log, (existsSync(log) ? readFileSync(log, "utf8") : "") + `${s}=${state(s)}\n`);
      ctx.ui.notify(`probe: ${s}=${state(s)}`, "info");
    },
  });
}
